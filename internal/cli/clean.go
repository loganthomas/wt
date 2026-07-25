// wt clean (PLAN.md Phase 6): reap what is provably finished —
// prunable registrations, merged trees, dead leases, orphaned
// state records — and nothing else. Every action is printed, -n
// previews them all, and the guards that fence wt done fence the
// same removals here (R2, R8).
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/lease"
	"github.com/loganthomas/wt/internal/pool"
	"github.com/loganthomas/wt/internal/state"
)

func newCleanCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Reap merged trees, dead leases, and stale state",
		Long: "Prune worktree registrations whose directories are gone,\n" +
			"remove managed trees whose branches are merged into the base\n" +
			"(with the same safety guards as wt done), release pool leases\n" +
			"whose sessions are provably dead, and drop recorded state for\n" +
			"trees that no longer exist. Every action is printed;\n" +
			"-n previews them all without performing any.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runClean(cmd, dry)
		},
	}
	cmd.Flags().BoolVarP(&dry, "dry-run", "n", false, "print what would be cleaned, do nothing")
	return cmd
}

// cleaner carries one clean run's seams: the open repo, its state
// dir, the output, and whether this is a preview. The acted flag
// distinguishes "nothing to clean" from a quiet success.
type cleaner struct {
	*wtRepo
	g       *gitx.Git
	st      state.Dir
	dry     bool
	chatter io.Writer
	acted   bool
}

func runClean(cmd *cobra.Command, dry bool) error {
	ctx := cmd.Context()
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	st, err := w.stateDir()
	if err != nil {
		return err
	}
	c := &cleaner{
		wtRepo:  w,
		g:       gitx.New(w.repo.Root),
		st:      st,
		dry:     dry,
		chatter: cmd.ErrOrStderr(),
	}
	trees, err := c.pruneWorktrees(ctx)
	if err != nil {
		return err
	}
	// Each step hands the survivors forward, so the state scan sees
	// the post-reap world without another git call.
	trees, err = c.reapMergedTrees(ctx, trees)
	if err != nil {
		return err
	}
	if err := c.reapDeadLeases(trees); err != nil {
		return err
	}
	if err := c.dropOrphanedState(trees); err != nil {
		return err
	}
	if !c.acted {
		fmt.Fprintln(c.chatter, "nothing to clean")
	}
	return nil
}

// act prints one performed or previewed action, pre-tensed by the
// caller ("pruned …" / "would prune …").
func (c *cleaner) act(format string, args ...any) {
	c.acted = true
	fmt.Fprintf(c.chatter, format+"\n", args...)
}

// pruneWorktrees clears registrations whose directories are gone
// from disk and returns the surviving trees. git's own prunable
// flag is the source of truth; wt only reports what git will do.
func (c *cleaner) pruneWorktrees(ctx context.Context) ([]gitx.Worktree, error) {
	trees, err := c.g.Worktrees(ctx)
	if err != nil {
		return nil, err
	}
	pruned := false
	for _, t := range trees {
		if !t.Prunable {
			continue
		}
		pruned = true
		if c.dry {
			c.act("would prune %s (gone from disk)", t.Path)
		} else {
			c.act("pruned %s (gone from disk)", t.Path)
		}
	}
	if !pruned || c.dry {
		return trees, nil
	}
	if err := c.g.WorktreePrune(ctx); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(trees, func(t gitx.Worktree) bool { return t.Prunable }), nil
}

// reapMergedTrees removes managed trees whose branches are merged
// into the base, exactly as wt done would: same guards, same
// removal, same branch delete. Only trees directly under the trees
// dir are candidates — the main checkout and hand-made trees
// elsewhere are not wt's to reap — and slot names are excluded
// unconditionally: slots are released, never removed (D14). A tree
// a guard refuses is skipped with the reason, never forced.
func (c *cleaner) reapMergedTrees(
	ctx context.Context, trees []gitx.Worktree,
) ([]gitx.Worktree, error) {
	base := c.cfg.Base
	if !c.g.HasCommit(ctx, base) {
		fmt.Fprintf(c.chatter,
			"base %q does not resolve to a commit — skipping merged-tree cleanup\n", base)
		return trees, nil
	}
	baseSHA, err := c.g.RevParse(ctx, base)
	if err != nil {
		return nil, err
	}
	survivors := make([]gitx.Worktree, 0, len(trees))
	for _, t := range trees {
		reaped, err := c.reapIfMerged(ctx, t, base, baseSHA[0])
		if err != nil {
			return nil, err
		}
		if !reaped || c.dry {
			survivors = append(survivors, t)
		}
	}
	return survivors, nil
}

// reapIfMerged handles one tree, reporting whether it was removed
// (or would be, in a dry run). A branch sitting exactly on the
// base tip is left alone: a freshly created tree and a
// fast-forward-merged branch are indistinguishable there, so only
// branches strictly behind the base count as merged.
func (c *cleaner) reapIfMerged(
	ctx context.Context, t gitx.Worktree, base, baseSHA string,
) (bool, error) {
	name, managed := c.treeStateName(t.Path)
	if !managed || pool.IsSlotName(name) || t.Branch == "" || t.Branch == base {
		return false, nil
	}
	if t.Head == baseSHA {
		return false, nil
	}
	merged, err := c.g.IsAncestor(ctx, t.Branch, base)
	if err != nil || !merged {
		return false, err
	}
	if c.dry {
		c.act("would remove %s (branch %s is merged into %s)", t.Path, t.Branch, base)
		return true, nil
	}
	if err := c.removeMerged(ctx, t, base); err != nil {
		if exitCodeFor(err) != exitPrecondition {
			return false, err
		}
		c.act("skipping %s: %v", t.Path, err)
		return false, nil
	}
	return true, nil
}

// removeMerged runs the wt done sequence on one merged tree. The
// unpushed-commit guard is deliberately absent: every commit on the
// branch is reachable from the base — that is what merged means —
// so deleting the branch strands nothing (R2).
func (c *cleaner) removeMerged(ctx context.Context, t gitx.Worktree, base string) error {
	if err := checkRemovable(t); err != nil {
		return err
	}
	pristine, err := finishGuards(ctx, c.repo.Root, t, c.cfg.Copy)
	if err != nil {
		return err
	}
	c.acted = true
	if err := c.removeTree(ctx, c.g, t, pristine, c.chatter); err != nil {
		return err
	}
	return finishBranch(ctx, c.g, t.Branch, true, c.chatter)
}

// reapDeadLeases frees slots whose leases are provably dead (D15):
// the pid is gone or reused, never a wall-clock guess. The lease
// is repinned to this session before release, the same protocol as
// wt release, so a racing claim can never be double-freed. An
// unreadable record proves nothing and is left with a pointer at
// the documented escape hatch.
func (c *cleaner) reapDeadLeases(trees []gitx.Worktree) error {
	leases := c.st.LeasesDir()
	slots, err := lease.Slots(leases)
	if err != nil {
		return err
	}
	for _, slot := range slots {
		held, err := lease.Get(leases, slot)
		if err != nil {
			fmt.Fprintf(c.chatter,
				"%s lease record unreadable — `wt release %s` clears it\n", slot, slot)
			continue
		}
		if held == nil || !held.Stale() {
			continue
		}
		if c.dry {
			c.act("would release %s (dead pid %d, was %s)", slot, held.PID, held.Branch)
			continue
		}
		if err := c.releaseDead(trees, slot, held); err != nil {
			return err
		}
	}
	return nil
}

// releaseDead clears one provably dead lease. The tree itself is
// left exactly as the dead session left it: a branch stays
// reachable, and the next claim's reset (with its guards) decides
// what survives, exactly as a claim-time steal would.
func (c *cleaner) releaseDead(trees []gitx.Worktree, slot string, held *lease.Info) error {
	pinned, err := lease.Repin(c.st.LeasesDir(), slot, lease.Cleaning, held)
	if err != nil {
		if isHeld(err) {
			// A claim raced in and holds the slot live now; that is
			// the freshest possible state, nothing left to reap.
			return nil
		}
		return err
	}
	// A slot with no tree behind it has state describing nothing;
	// dropped while the pin still holds, as releaseVacantSlot does.
	if _, registered := findTree(trees, filepath.Join(c.treesDir(), slot)); !registered {
		if err := c.st.RemoveTree(slot); err != nil {
			return err
		}
	}
	if err := lease.Release(c.st.LeasesDir(), slot, pinned); err != nil {
		return err
	}
	c.act("released %s (dead pid %d, was %s)", slot, held.PID, held.Branch)
	return nil
}

// dropOrphanedState removes recorded state for trees git no longer
// lists (R8): a tree deleted out of band leaves its refresh hash
// and markers behind, and a later namesake must not inherit them.
// Names with a lease directory are skipped — a mid-provision slot
// has state before its worktree registers, and the lease is what
// proves someone is working.
func (c *cleaner) dropOrphanedState(trees []gitx.Worktree) error {
	names, err := c.st.TreeNames()
	if err != nil {
		return err
	}
	leased, err := lease.Slots(c.st.LeasesDir())
	if err != nil {
		return err
	}
	for _, name := range names {
		if slices.Contains(leased, name) {
			continue
		}
		if _, registered := findTree(trees, filepath.Join(c.treesDir(), name)); registered {
			continue
		}
		if c.dry {
			c.act("would drop stale state for %s", name)
			continue
		}
		if err := c.st.RemoveTree(name); err != nil {
			return err
		}
		c.act("dropped stale state for %s", name)
	}
	return nil
}
