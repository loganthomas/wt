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
	if err := c.reapMergedTrees(ctx, trees); err != nil {
		return err
	}
	if err := c.reapDeadLeases(ctx); err != nil {
		return err
	}
	if err := c.dropOrphanedState(ctx); err != nil {
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
// flag is the source of truth; wt only reports what git has done.
// The survivors exclude pruned registrations in the dry run too,
// so the preview of every later step sees the same post-prune
// world a real run would.
func (c *cleaner) pruneWorktrees(ctx context.Context) ([]gitx.Worktree, error) {
	trees, err := c.g.Worktrees(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(trees, func(t gitx.Worktree) bool { return t.Prunable }) {
		return trees, nil
	}
	if !c.dry {
		if err := c.g.WorktreePrune(ctx); err != nil {
			return nil, err
		}
	}
	for _, t := range trees {
		if !t.Prunable {
			continue
		}
		if c.dry {
			c.act("would prune %s (gone from disk)", t.Path)
		} else {
			c.act("pruned %s (gone from disk)", t.Path)
		}
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
func (c *cleaner) reapMergedTrees(ctx context.Context, trees []gitx.Worktree) error {
	base := c.cfg.Base
	// One rev-parse answers "does the base resolve" and "where is
	// it", and one for-each-ref yields every merged branch, so the
	// per-tree loop below spawns no git at all for the common
	// not-merged case. --verify --end-of-options, because plain
	// rev-parse echoes a dash-prefixed name back verbatim (option
	// passthrough) — a garbage "SHA" that would defeat the
	// fresh-tree guard below instead of skipping the cleanup.
	baseSHA, err := c.g.RevParse(ctx, "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		fmt.Fprintf(c.chatter,
			"base %q does not resolve to a commit — skipping merged-tree cleanup\n", base)
		return nil
	}
	merged, err := c.g.MergedBranches(ctx, base)
	if err != nil {
		return err
	}
	var doomed []string
	for _, t := range trees {
		branch, err := c.reapIfMerged(ctx, t, base, baseSHA[0], merged)
		if err != nil {
			return err
		}
		if branch != "" && !slices.Contains(doomed, branch) {
			doomed = append(doomed, branch)
		}
	}
	// Branches go only after the whole sweep: one merged branch can
	// be checked out in several trees (git worktree add -f), and a
	// delete attempted while a later tree still held it would abort
	// the run half-done. A delete git still refuses (the branch
	// lingers in some unmanaged tree) is reported, not fatal: the
	// reaped trees are gone and every commit stays reachable.
	for _, branch := range doomed {
		if err := finishBranch(ctx, c.g, branch, true, c.chatter); err != nil {
			c.act("could not delete branch %s: %v", branch, err)
		}
	}
	return nil
}

// reapIfMerged handles one tree. A branch sitting exactly on the
// base tip is left alone: a freshly created tree and a
// fast-forward-merged branch are indistinguishable there, so only
// branches strictly behind the base count as merged. The main
// checkout is excluded by identity, exactly as wt done excludes
// it: a trees_dir that contains the repo root (say "..") must not
// make the user's primary checkout read as reapable.
func (c *cleaner) reapIfMerged(
	ctx context.Context, t gitx.Worktree, base, baseSHA string, merged map[string]bool,
) (string, error) {
	name, isManaged := c.treeStateName(t.Path)
	if !isManaged || pool.IsSlotName(name) || t.Path == c.repo.Root ||
		t.Branch == "" || t.Branch == base {
		return "", nil
	}
	if t.Head == baseSHA || !merged[t.Branch] {
		return "", nil
	}
	branch, err := c.reapMergedTree(ctx, t, base)
	if err != nil {
		if exitCodeFor(err) != exitPrecondition {
			return "", err
		}
		c.act("skipping %s: %v", t.Path, err)
		return "", nil
	}
	return branch, nil
}

// reapMergedTree runs the wt done sequence on one merged tree. The
// unpushed-commit guard is deliberately absent: every commit on the
// branch is reachable from the base — that is what merged means —
// so deleting the branch strands nothing (R2). The other guards run
// in previews too: they are read-only, and -n must promise only
// what a real run would do — a dirty merged tree is skipped in both.
// It returns the branch whose tree it removed, for the caller's
// deferred delete; "" when nothing was removed (a preview included:
// -n deletes no branches, so it has none to defer).
func (c *cleaner) reapMergedTree(
	ctx context.Context, t gitx.Worktree, base string,
) (string, error) {
	if err := checkRemovable(t); err != nil {
		return "", err
	}
	pristine, err := finishGuards(ctx, c.repo.Root, t, c.cfg.Copy)
	if err != nil {
		return "", err
	}
	if c.dry {
		c.act("would remove %s (branch %s is merged into %s)", t.Path, t.Branch, base)
		return "", nil
	}
	c.acted = true
	if err := c.removeTree(ctx, c.g, t, pristine, c.chatter); err != nil {
		return "", err
	}
	return t.Branch, nil
}

// reapDeadLeases frees slots whose leases are provably dead (D15):
// the pid is gone or reused, never a wall-clock guess. The lease
// is repinned to this session before release, the same protocol as
// wt release, so a racing claim can never be double-freed. An
// unreadable record proves nothing and is left with a pointer at
// the documented escape hatch.
func (c *cleaner) reapDeadLeases(ctx context.Context) error {
	leases := c.st.LeasesDir()
	slots, err := lease.Slots(leases)
	if err != nil {
		return err
	}
	for _, slot := range slots {
		held, err := lease.Get(leases, slot)
		if err != nil {
			// Through act, not raw chatter: a run whose only event is
			// this notice must not also claim there was nothing to
			// clean.
			c.act("%s %s", slot, unreadableLeaseAdvice(slot))
			continue
		}
		if held == nil || !held.Stale() {
			continue
		}
		if c.dry {
			c.act("would release %s (dead pid %d, was %s)", slot, held.PID, held.Branch)
			continue
		}
		if err := c.releaseDead(ctx, slot, held); err != nil {
			return err
		}
	}
	return nil
}

// releaseDead clears one provably dead lease. The tree itself is
// left exactly as the dead session left it: a branch stays
// reachable, and the next claim's reset (with its guards) decides
// what survives, exactly as a claim-time steal would.
func (c *cleaner) releaseDead(ctx context.Context, slot string, held *lease.Info) error {
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
	// Listed under the pin: a claim that came and went since the
	// run's first listing may have provisioned the tree, and state
	// a live tree depends on must survive.
	trees, err := c.liveTrees(ctx)
	if err != nil {
		return err
	}
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

// liveTrees lists the worktrees as every post-prune step sees
// them: a prunable registration is already gone in a real run,
// and counts as gone in a preview so -n reports the same world.
func (c *cleaner) liveTrees(ctx context.Context) ([]gitx.Worktree, error) {
	trees, err := c.g.Worktrees(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(trees, func(t gitx.Worktree) bool { return t.Prunable }), nil
}

// dropOrphanedState removes recorded state for trees git no longer
// lists (R8): a tree deleted out of band leaves its refresh hash
// and markers behind, and a later namesake must not inherit them.
// Names with a lease directory are skipped — a mid-provision slot
// has state before its worktree registers, and the lease is what
// proves someone is working. The listing is fresh: a claim that
// completed since the run started registered its tree, and that
// tree's state must survive.
func (c *cleaner) dropOrphanedState(ctx context.Context) error {
	trees, err := c.liveTrees(ctx)
	if err != nil {
		return err
	}
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
