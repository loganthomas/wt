// Pool-mode orchestration: claiming, provisioning, resetting, and
// releasing slots (PLAN.md Phase 4). The lease is always taken
// before any git runs and dropped on any failure; the pattern
// guard fences every destructive step (D14); and the refresh gate
// is what turns a claim into seconds instead of minutes (D5).
package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/guard"
	"github.com/loganthomas/wt/internal/lease"
	"github.com/loganthomas/wt/internal/pool"
	"github.com/loganthomas/wt/internal/state"
)

// poolRepo is an open repo whose config enables pool mode,
// plus the seams every slot operation needs.
type poolRepo struct {
	*wtRepo
	g     *gitx.Git // rooted at the main checkout
	state state.Dir
}

// poolOf upgrades an open repo to pool operations, or explains
// why it cannot: the repo is fine, the mode is off (exit 3).
func poolOf(w *wtRepo) (*poolRepo, error) {
	if w.cfg.Pool == nil {
		return nil, preconditionf(
			"pool mode is not enabled here — rerun `wt init`, or add a [pool] table to wt.toml")
	}
	st, err := w.stateDir()
	if err != nil {
		return nil, err
	}
	return &poolRepo{wtRepo: w, g: gitx.New(w.repo.Root), state: st}, nil
}

// claimSlot runs a whole claim: lease a slot, provision or reset
// it, attach the branch, port the copy files, refresh behind the
// lockfile gate. It returns the slot's path — the machine product
// of every claim (D13). Any failure drops the lease again, so a
// failed claim can never shrink the usable pool — and a slot the
// guards refuse (say, stranded orphan commits) is skipped with a
// notice rather than blocking every claim while healthy slots
// sit free (R3).
func (p *poolRepo) claimSlot(
	ctx context.Context, branch, base string, chatter io.Writer,
) (string, error) {
	skip := make(map[string]bool)
	for {
		slot, reclaimed, err := p.acquire(branch, skip)
		if err != nil {
			return "", err
		}
		if reclaimed != nil {
			fmt.Fprintf(chatter, "reclaimed %s from a dead session (pid %d, was %s)\n",
				slot, reclaimed.PID, reclaimed.Branch)
		}
		dest, err := p.prepareSlot(ctx, slot, branch, base, chatter)
		if err == nil {
			fmt.Fprintf(chatter, "claimed %s for %s\n", slot, branch)
			return dest, nil
		}
		_ = lease.Release(p.state.LeasesDir(), slot)
		if exitCodeFor(err) != exitPrecondition {
			return "", err
		}
		fmt.Fprintf(chatter, "skipping %s: %v\n", slot, err)
		skip[slot] = true
	}
}

// prepareSlot readies one leased slot for branch: provision or
// reset, branch attach, copies, gated refresh.
func (p *poolRepo) prepareSlot(
	ctx context.Context, slot, branch, base string, chatter io.Writer,
) (string, error) {
	dest := filepath.Join(p.treesDir(), slot)
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return "", err
	}
	t, registered := findTree(trees, dest)
	switch {
	case registered && t.Prunable:
		return "", preconditionf(
			"%s is registered but gone from disk — `git worktree prune`, then claim again", dest)
	case registered && !p.state.Provisioned(slot):
		// A provision died between worktree-add and its marker:
		// the slot looks real but its setup hook never finished,
		// and a plain reset would skip setup forever. Redo it from
		// scratch — nothing but base content and half-built
		// artifacts can be in there, with the orphan guard as the
		// backstop for anything committed by hand.
		if t.Detached {
			if err := guard.CheckOrphans(ctx, t.Path); err != nil {
				return "", err
			}
		}
		fmt.Fprintf(chatter, "reprovisioning %s (an earlier provision did not complete)\n", slot)
		if err := p.g.WorktreeRemoveForce(ctx, dest); err != nil {
			return "", err
		}
		if err := p.state.RemoveTree(slot); err != nil {
			return "", err
		}
		if err := p.provisionSlot(ctx, slot, base, chatter); err != nil {
			return "", err
		}
	case registered:
		if err := p.resetSlot(ctx, t, base, chatter); err != nil {
			return "", err
		}
	default:
		if err := p.provisionSlot(ctx, slot, base, chatter); err != nil {
			return "", err
		}
	}

	sg := gitx.New(dest)
	if p.g.HasCommit(ctx, "refs/heads/"+branch) {
		err = sg.Switch(ctx, branch)
	} else {
		// The slot is parked at base, so creating here branches
		// off exactly what wt new promises.
		err = sg.SwitchCreate(ctx, branch)
	}
	if err != nil {
		return "", err
	}

	if err := copyFiles(ctx, p.repo.Root, dest, p.cfg.Copy, chatter); err != nil {
		return "", err
	}
	if err := refreshTree(ctx, p.cfg, p.state, dest, slot, chatter); err != nil {
		return "", err
	}
	return dest, nil
}

// acquire leases the first available slot outside skip: free ones
// first, then provably dead ones, so a crashed session's leftovers
// survive as long as the pool has other room. It reports a stolen
// lease's old record so the caller can say what was reclaimed.
func (p *poolRepo) acquire(branch string, skip map[string]bool) (string, *lease.Info, error) {
	leases := p.state.LeasesDir()
	names := pool.Names(p.cfg.Pool.Size)
	for _, slot := range names {
		if skip[slot] {
			continue
		}
		if held, err := lease.Get(leases, slot); err != nil || held != nil {
			continue
		}
		if err := lease.Acquire(leases, slot, branch); err == nil {
			return slot, nil, nil
		}
		// Lost the race for this slot; keep scanning.
	}
	for _, slot := range names {
		if skip[slot] {
			continue
		}
		old, err := lease.Get(leases, slot)
		if err != nil || old == nil || !old.Stale() {
			continue
		}
		if err := lease.Acquire(leases, slot, branch); err == nil {
			return slot, old, nil
		}
	}
	size := p.cfg.Pool.Size
	if len(skip) > 0 {
		return "", nil, preconditionf(
			"no usable slot in the pool of %d (%d blocked, reasons above) — "+
				"resolve a blocked slot, or `wt pool resize %d`", size, len(skip), size+1)
	}
	return "", nil, preconditionf(
		"no free slot in the pool of %d — `wt pool ls` shows the holders; "+
			"`wt done` a finished one, or `wt pool resize %d`", size, size+1)
}

// resetSlot parks a slot back at base: forced detach, then clean
// without -x, so gitignored caches stay warm (D14). The pattern
// guard runs first — reset is the destructive primitive, and only
// true slot paths may ever reach it — and the orphan guard keeps
// a detached slot's commits recoverable (R2). Everything else
// uncommitted is discarded with notice: it belongs to no live
// session, or its session already passed the release guards.
func (p *poolRepo) resetSlot(
	ctx context.Context, t gitx.Worktree, base string, chatter io.Writer,
) error {
	slot, ok := pool.SlotPath(p.treesDir(), t.Path)
	if !ok {
		return fmt.Errorf("refusing to reset %s: not a pool slot under %s", t.Path, p.treesDir())
	}
	if t.Detached {
		if err := guard.CheckOrphans(ctx, t.Path); err != nil {
			return err
		}
	}
	sg := gitx.New(t.Path)
	entries, err := sg.Status(ctx)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		fmt.Fprintf(chatter, "%s: discarding %d leftover uncommitted change(s)\n",
			slot, len(entries))
	}
	if err := sg.CheckoutDetach(ctx, base); err != nil {
		return err
	}
	return sg.CleanUntracked(ctx)
}

// releaseSlot finishes work in a slot: guards, park at base,
// optional branch delete, lease drop. The tree itself always
// stays — a warm slot is the entire point of the pool.
func (p *poolRepo) releaseSlot(
	ctx context.Context, t gitx.Worktree, slot string, deleteBranch bool, chatter io.Writer,
) error {
	leases := p.state.LeasesDir()
	held, err := lease.Get(leases, slot)
	// An unreadable record still occupies the slot — claim never
	// steals what it cannot prove dead — so release, the documented
	// escape hatch, must proceed and clear it rather than bounce
	// off "not claimed".
	if err == nil && held == nil && t.Detached {
		return preconditionf("%s is not claimed — nothing to release", slot)
	}
	if err != nil {
		held = nil
	}
	// Pin the lease to this session before any guard or reset runs:
	// past this point no concurrent claim can steal the slot, and
	// the lease removed at the end is provably the one handled
	// here. A failure after the pin simply leaves the slot claimed
	// by this session — truthful, retryable, and self-expiring if
	// the session dies.
	if err := lease.Repin(leases, slot, cmp.Or(t.Branch, "(releasing)"), held); err != nil {
		var heldErr *lease.HeldError
		if errors.As(err, &heldErr) {
			return preconditionf("%v — the slot changed hands; let that claim finish", heldErr)
		}
		return err
	}

	// The same protections as wt done on a personal tree (R2):
	// pristine planted copies are wt's own and reset freely,
	// an edited one is user data the clean would destroy.
	pristine, edited, err := splitCopies(ctx, p.repo.Root, t.Path, p.cfg.Copy)
	if err != nil {
		return err
	}
	if len(edited) > 0 {
		return preconditionf(
			"%s: the planted copy %s no longer matches the main checkout — "+
				"back it up, or make the two match first", t.Path, edited[0])
	}
	if err := guard.CheckDirty(ctx, t.Path, pristine...); err != nil {
		return err
	}
	if t.Detached {
		if err := guard.CheckOrphans(ctx, t.Path); err != nil {
			return err
		}
	}
	if deleteBranch && t.Branch != "" {
		if err := guard.CheckUnpushed(ctx, t.Path, p.cfg.Base); err != nil {
			return err
		}
	}

	if err := p.resetSlot(ctx, t, p.cfg.Base, chatter); err != nil {
		return err
	}
	if err := lease.Release(leases, slot); err != nil {
		return err
	}
	fmt.Fprintf(chatter, "released %s\n", slot)
	switch {
	case deleteBranch && t.Branch != "":
		if err := p.g.DeleteBranch(ctx, t.Branch); err != nil {
			return err
		}
		fmt.Fprintf(chatter, "deleted branch %s\n", t.Branch)
	case t.Branch != "":
		fmt.Fprintf(chatter, "kept branch %s\n", t.Branch)
	}
	return nil
}

// provisionSlot creates and warms one slot: a detached worktree
// at base, ported copies, the setup hook, and the refresh hash
// recorded so the first claim doesn't redo what setup just did.
// A failed provision rolls the worktree back: a half-provisioned
// slot that skipped its setup hook would poison every later
// claim, and a clean retry is cheap by comparison.
func (p *poolRepo) provisionSlot(
	ctx context.Context, slot, base string, chatter io.Writer,
) error {
	dest := filepath.Join(p.treesDir(), slot)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := p.g.WorktreeAddDetach(ctx, dest, base); err != nil {
		return err
	}
	if err := p.warmSlot(ctx, dest, slot, chatter); err != nil {
		_ = p.g.WorktreeRemoveForce(ctx, dest)
		_ = p.state.RemoveTree(slot)
		return fmt.Errorf("provisioning %s failed: %w", slot, err)
	}
	fmt.Fprintf(chatter, "provisioned %s (detached at %s)\n", slot, base)
	return nil
}

func (p *poolRepo) warmSlot(ctx context.Context, dest, slot string, chatter io.Writer) error {
	if err := copyFiles(ctx, p.repo.Root, dest, p.cfg.Copy, chatter); err != nil {
		return err
	}
	if setup := p.cfg.Hooks.Setup; setup != "" {
		fmt.Fprintf(chatter, "running setup hook: %s\n", setup)
		if err := runHook(ctx, dest, setup, chatter); err != nil {
			return fmt.Errorf("setup hook failed: %w", err)
		}
	}
	if files := p.cfg.Hooks.RefreshIfChanged; len(files) > 0 {
		hash, err := pool.Hash(dest, files)
		if err != nil {
			return err
		}
		if err := p.state.WriteRefreshHash(slot, hash); err != nil {
			return err
		}
	}
	return p.state.MarkProvisioned(slot)
}

// provisionPool warms slots from+1 through to, holding each
// slot's lease while it builds so a concurrent claim can never
// grab a half-built slot. Used by wt init and wt pool resize.
func (p *poolRepo) provisionPool(ctx context.Context, from, to int, chatter io.Writer) error {
	leases := p.state.LeasesDir()
	for i := from + 1; i <= to; i++ {
		slot := pool.SlotName(i)
		if err := lease.Acquire(leases, slot, "(provisioning)"); err != nil {
			return err
		}
		err := p.provisionSlot(ctx, slot, p.cfg.Base, chatter)
		if rerr := lease.Release(leases, slot); err == nil {
			err = rerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// refreshTree runs hooks.refresh behind the lockfile gate (D5):
// with refresh_if_changed configured, only a hash change triggers
// it, and only success re-records the hash; without the gate it
// runs on every claim and new. Shared by pool claims and
// default-mode trees so the two can never drift.
func refreshTree(
	ctx context.Context, cfg config.Config, st state.Dir, dest, name string, chatter io.Writer,
) error {
	refresh := cfg.Hooks.Refresh
	if refresh == "" {
		return nil
	}
	files := cfg.Hooks.RefreshIfChanged
	var current string
	if len(files) > 0 {
		var err error
		current, err = pool.Hash(dest, files)
		if err != nil {
			return err
		}
		if current == st.RefreshHash(name) {
			return nil
		}
	}
	fmt.Fprintf(chatter, "running refresh hook: %s\n", refresh)
	if err := runHook(ctx, dest, refresh, chatter); err != nil {
		return fmt.Errorf("refresh hook failed: %w", err)
	}
	if len(files) > 0 {
		return st.WriteRefreshHash(name, current)
	}
	return nil
}

// findTree looks a path up in git's worktree list.
func findTree(trees []gitx.Worktree, path string) (gitx.Worktree, bool) {
	for _, t := range trees {
		if t.Path == path {
			return t, true
		}
	}
	return gitx.Worktree{}, false
}

// errIsHeld reports whether err is a lease.HeldError.
func errIsHeld(err error) bool {
	var held *lease.HeldError
	return errors.As(err, &held)
}
