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
	"strings"

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

// openPool is the pool commands' shared preamble: resolve the
// repo, then require pool mode.
func openPool(ctx context.Context) (*poolRepo, error) {
	w, err := openRepo(ctx)
	if err != nil {
		return nil, err
	}
	return poolOf(w)
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
// lockfile gate. It returns the slot's path, the machine product
// of every claim (D13). A failure drops the lease again, so a
// failed claim does not shrink the usable pool, with one honest
// exception: when even re-parking the slot fails, the lease is
// kept on the session so the slot's condition stays visible. A
// slot the guards refuse (say, stranded orphan commits) is
// skipped with a notice rather than blocking every claim while
// healthy slots sit free (R3).
func (p *poolRepo) claimSlot(
	ctx context.Context, branch, base string, chatter io.Writer,
) (string, error) {
	skip := make(map[string]bool)
	for {
		slot, mine, reclaimed, err := p.acquire(branch, skip)
		if err != nil {
			return "", err
		}
		if reclaimed != nil {
			fmt.Fprintf(chatter, "reclaimed %s from a dead session (pid %d, was %s)\n",
				slot, reclaimed.PID, reclaimed.Branch)
		}
		dest, err := p.prepareSlot(ctx, slot, branch, base, chatter)
		if err == nil {
			// The handoff: the slot was held by wt itself while it
			// provisioned; now the session doing the work takes over
			// (see the lease package comment).
			if _, err := lease.Repin(p.state.LeasesDir(), slot, branch, mine); err != nil {
				var heldErr *lease.HeldError
				if errors.As(err, &heldErr) {
					return "", preconditionf(
						"%v — the slot changed hands mid-claim; rerun the claim", heldErr)
				}
				return "", err
			}
			fmt.Fprintf(chatter, "claimed %s for %s\n", slot, branch)
			return dest, nil
		}
		if rerr := p.reparkSlot(ctx, slot, branch, base); rerr != nil {
			// The lease so far names wt itself and dies with it;
			// only a repin to the session makes "keeps its claim"
			// true past this process's exit.
			if _, perr := lease.Repin(p.state.LeasesDir(), slot, branch, mine); perr != nil {
				return "", fmt.Errorf(
					"%w — re-parking also failed (%v); `wt release %s` clears the slot",
					err, rerr, slot)
			}
			return "", fmt.Errorf(
				"%w — re-parking also failed (%v); the slot keeps its claim, "+
					"`wt release %s` clears it", err, rerr, slot)
		}
		_ = lease.Release(p.state.LeasesDir(), slot, mine)
		if exitCodeFor(err) != exitPrecondition {
			return "", err
		}
		fmt.Fprintf(chatter, "skipping %s: %v\n", slot, err)
		skip[slot] = true
	}
}

// reparkSlot returns a slot to its parked state after a claim
// failed partway, but only when the failure struck after this
// claim's own branch attach: dropping the lease with the branch
// still checked out would bounce retries off "already checked
// out" while concurrent claims silently reset the tree. Slots the
// guards refused are left exactly as found: a forced detach
// there would destroy the very state the guard protected. When
// the re-park itself fails, the caller repins the lease to the
// session so the slot's condition stays visibly claimed instead
// of expiring with wt and being silently reset by the next claim.
func (p *poolRepo) reparkSlot(ctx context.Context, slot, branch, base string) error {
	dest := filepath.Join(p.treesDir(), slot)
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	t, registered := findTree(trees, dest)
	if !registered || t.Branch != branch {
		return nil
	}
	return gitx.New(dest).CheckoutDetach(ctx, base)
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
	case registered && t.Detached && !p.state.Provisioned(slot):
		// A provision died between worktree-add and its marker:
		// the slot looks real but its setup hook never finished,
		// and a plain reset would skip setup forever. Redo it from
		// scratch: nothing but base content and half-built
		// artifacts can be in there, with the orphan guard as the
		// backstop for anything committed by hand. Detached only:
		// provisioning never attaches a branch, so a branch on an
		// unmarked slot proves the recorded state was lost, not
		// that the tree is disposable; it takes the reset path
		// below, which keeps branch commits reachable and says
		// what it discards instead of force-removing silently.
		if err := guard.CheckOrphans(ctx, t.Path); err != nil {
			return "", err
		}
		fmt.Fprintf(chatter, "reprovisioning %s (an earlier provision did not complete)\n", slot)
		if err := p.g.WorktreeRemoveForce(ctx, dest); err != nil {
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
		// An unregistered slot may have just been removed by a
		// shrink working from a newer config than this claim
		// loaded; re-read the size before materializing a tree the
		// pool no longer owns; it would be invisible to pool ls.
		if err := p.checkStillInPool(slot); err != nil {
			return "", err
		}
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
// survive as long as the pool has other room. It returns the lease
// record it wrote, and a stolen lease's old record so the caller
// can say what was reclaimed.
func (p *poolRepo) acquire(
	branch string, skip map[string]bool,
) (string, *lease.Info, *lease.Info, error) {
	leases := p.state.LeasesDir()
	names := pool.Names(p.cfg.Pool.Size)
	for _, slot := range names {
		if skip[slot] {
			continue
		}
		if held, err := lease.Get(leases, slot); err != nil || held != nil {
			continue
		}
		mine, err := lease.Acquire(leases, slot, branch)
		if err == nil {
			return slot, mine, nil, nil
		}
		if !isHeld(err) {
			// Only a lost race reads as "keep scanning"; an I/O
			// failure would repeat on every slot and must not be
			// dressed up as a full pool.
			return "", nil, nil, err
		}
	}
	for _, slot := range names {
		if skip[slot] {
			continue
		}
		old, err := lease.Get(leases, slot)
		if err == nil && (old == nil || !old.Stale()) {
			continue
		}
		// A stale lease, or one Get cannot read: hand the slot to
		// Acquire, which under its lock steals the provably dead,
		// reclaims the recordless (a claimer that died between
		// mkdir and record write), and refuses everything else.
		// Filtering unreadable leases out here left that reclaim
		// unreachable, wedging the slot until a manual release.
		mine, aerr := lease.Acquire(leases, slot, branch)
		if aerr == nil {
			return slot, mine, old, nil
		}
		if !isHeld(aerr) {
			return "", nil, nil, aerr
		}
	}
	size := p.cfg.Pool.Size
	if len(skip) > 0 {
		return "", nil, nil, preconditionf(
			"no usable slot in the pool of %d (%d blocked, reasons above) — "+
				"resolve a blocked slot, or `wt pool resize %d`", size, len(skip), size+1)
	}
	return "", nil, nil, preconditionf(
		"no free slot in the pool of %d — `wt pool ls` shows the holders; "+
			"`wt done` a finished one, or `wt pool resize %d`", size, size+1)
}

// resetSlot parks a slot back at base: forced detach, then clean
// without -x, so gitignored caches stay warm (D14). The pattern
// guard runs first (reset is the destructive primitive, and only
// true slot paths may ever reach it), and the orphan guard keeps
// a detached slot's commits recoverable (R2). Everything else
// uncommitted is discarded with notice: it belongs to no live
// session, or its session already passed the release guards.
func (p *poolRepo) resetSlot(
	ctx context.Context, t gitx.Worktree, base string, chatter io.Writer,
) error {
	slot, err := p.requireSlot(t.Path, "reset")
	if err != nil {
		return err
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
	for _, e := range entries {
		// git status collapses an untracked nested repository to
		// "dir/" even under -uall; its commits live only in that
		// nested .git, invisible to the orphan guard, and the
		// forced clean would destroy them. Refuse like any other
		// guard: the claim skips the slot, and a human decides.
		if !strings.HasSuffix(e.Path, "/") {
			continue
		}
		if _, err := os.Stat(filepath.Join(t.Path, e.Path, ".git")); err == nil {
			return preconditionf(
				"%s holds a nested git repository %s — a reset would destroy its "+
					"history; move it out or delete it by hand", slot, e.Path)
		}
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
// stays: a warm slot is the entire point of the pool.
func (p *poolRepo) releaseSlot(
	ctx context.Context, t gitx.Worktree, slot string, deleteBranch bool, chatter io.Writer,
) error {
	// Before the repin: an unresolvable base would otherwise fail
	// deep in the reset with a raw git error, after the lease has
	// already been rewritten onto this session.
	if err := checkBase(ctx, p.g, p.cfg.Base); err != nil {
		return err
	}
	// A parked slot with a branch still attached is releasable
	// (that is what parking fixes), so the claim requirement holds
	// only for detached trees.
	pinned, err := p.pinForRelease(slot, cmp.Or(t.Branch, lease.Releasing), t.Detached)
	if err != nil {
		return err
	}
	leases := p.state.LeasesDir()

	if _, err := finishGuards(ctx, p.repo.Root, t, p.cfg.Copy); err != nil {
		return err
	}
	if deleteBranch && t.Branch != "" {
		if err := guard.CheckUnpushed(ctx, t.Path, p.cfg.Base); err != nil {
			return err
		}
	}

	if err := p.resetSlot(ctx, t, p.cfg.Base, chatter); err != nil {
		return err
	}
	if err := lease.Release(leases, slot, pinned); err != nil {
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

// isHeld reports whether err is a lease refusal (the expected,
// scannable kind of Acquire failure) as opposed to a hard error.
func isHeld(err error) bool {
	var held *lease.HeldError
	return errors.As(err, &held)
}

// requireSlot is the D14 pattern guard at the door of every
// destructive slot operation: only a true slot path may pass,
// and the refusal names the operation it stopped.
func (p *poolRepo) requireSlot(path, verb string) (string, error) {
	slot, ok := pool.SlotPath(p.treesDir(), path)
	if !ok {
		return "", fmt.Errorf(
			"refusing to %s %s: not a pool slot under %s", verb, path, p.treesDir())
	}
	return slot, nil
}

// checkStillInPool re-reads the configured size and refuses a
// slot the pool no longer covers. Config trouble only costs the
// recheck: the claim then trusts the size it loaded at startup.
func (p *poolRepo) checkStillInPool(slot string) error {
	idx, ok := pool.SlotIndex(slot)
	if !ok {
		return fmt.Errorf("%s is not a slot name", slot)
	}
	merged, err := loadMerged(p.repo)
	if err != nil {
		return nil
	}
	if merged.Pool == nil || idx > merged.Pool.Size {
		return preconditionf(
			"the pool shrank below %s while this claim ran — rerun the claim", slot)
	}
	return nil
}

// pinForRelease transfers slot's lease to this session, the
// entry half of every release: past a successful pin no
// concurrent claim can steal the slot, and the lease dropped at
// the end is provably the one handled here. A failure after the
// pin simply leaves the slot claimed by this session: truthful,
// retryable, and self-expiring if the session dies. A free slot
// is refused when claimRequired; an unreadable record is taken
// over regardless, because claim never steals what it cannot
// prove dead and release is the documented way out.
func (p *poolRepo) pinForRelease(
	slot, branch string, claimRequired bool,
) (*lease.Info, error) {
	leases := p.state.LeasesDir()
	held, err := lease.Get(leases, slot)
	if err == nil && held == nil && claimRequired {
		return nil, preconditionf("%s is not claimed — nothing to release", slot)
	}
	if err != nil {
		held = nil
	}
	pinned, err := lease.Repin(leases, slot, branch, held)
	if err != nil {
		var heldErr *lease.HeldError
		if errors.As(err, &heldErr) {
			return nil, preconditionf(
				"%v — the slot changed hands; let that claim finish", heldErr)
		}
		return nil, err
	}
	return pinned, nil
}

// releaseVacantSlot clears a lease on a slot with no worktree
// behind it, via the same pin-then-drop protocol as a full
// release, minus the tree work there is no tree to do. The slot's
// recorded state goes too: with the tree gone it describes
// nothing, and a later provision must not inherit it.
func (p *poolRepo) releaseVacantSlot(slot string, chatter io.Writer) error {
	pinned, err := p.pinForRelease(slot, lease.Releasing, true)
	if err != nil {
		return err
	}
	// State goes while the pin still holds: dropped after the
	// lease, it could race a fresh claim and delete the marker
	// that claim just wrote.
	if err := p.state.RemoveTree(slot); err != nil {
		return err
	}
	if err := lease.Release(p.state.LeasesDir(), slot, pinned); err != nil {
		return err
	}
	fmt.Fprintf(chatter, "released %s\n", slot)
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
	// A leftover directory from a previous incarnation of the repo
	// (a re-clone next to a surviving trees dir) would make git's
	// worktree add fail with a raw "already exists"; name the
	// remedy instead, and keep the refusal at exit 3 so a claim
	// skips past it to healthy slots.
	if _, err := os.Stat(dest); err == nil {
		return preconditionf(
			"%s already exists but is not a registered worktree — "+
				"remove the leftover directory, then rerun", dest)
	}
	// Cleared before the worktree exists: a stale provisioned
	// marker still standing inside the add-to-warm crash window
	// would make a crashed provision read as complete. finishFresh
	// clears the rest of the stale state for every fresh tree.
	if err := p.state.RemoveTree(slot); err != nil {
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
	if err := finishFresh(ctx, p.cfg, p.state, dest, slot, chatter); err != nil {
		return err
	}
	return p.state.MarkProvisioned(slot)
}

// finishFresh completes a just-created tree or slot, one warm-up
// only: the setup hook when configured (presumed to leave the
// tree fully built, so the refresh hash it implicitly satisfied
// is recorded rather than immediately re-run), otherwise the
// refresh hook through its usual gate. Shared by wt new and slot
// provisioning so the two can never disagree on what "fresh"
// means.
func finishFresh(
	ctx context.Context, cfg config.Config, st state.Dir, dest, name string, chatter io.Writer,
) error {
	// Fresh also means no inherited state: a namesake tree removed
	// out of band can leave a refresh hash behind that would
	// satisfy the gate and skip the very warm-up this function
	// exists to run.
	if err := st.RemoveTree(name); err != nil {
		return err
	}
	setup := cfg.Hooks.Setup
	if setup == "" {
		return refreshTree(ctx, cfg, st, dest, name, chatter)
	}
	fmt.Fprintf(chatter, "running setup hook: %s\n", setup)
	if err := runHook(ctx, dest, setup, chatter); err != nil {
		return fmt.Errorf("setup hook failed: %w", err)
	}
	if files := cfg.Hooks.RefreshIfChanged; len(files) > 0 {
		hash, err := pool.Hash(dest, files)
		if err != nil {
			return err
		}
		return st.WriteRefreshHash(name, hash)
	}
	return nil
}

// provisionPool warms slots from+1 through to, holding each
// slot's lease while it builds so a concurrent claim can never
// grab a half-built slot. Used by wt init and wt pool resize.
func (p *poolRepo) provisionPool(ctx context.Context, from, to int, chatter io.Writer) error {
	leases := p.state.LeasesDir()
	for i := from + 1; i <= to; i++ {
		slot := pool.SlotName(i)
		mine, err := lease.Acquire(leases, slot, lease.Provisioning)
		if err != nil {
			return err
		}
		err = p.provisionSlot(ctx, slot, p.cfg.Base, chatter)
		if rerr := lease.Release(leases, slot, mine); err == nil {
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
