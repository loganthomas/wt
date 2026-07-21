package cli

import (
	"context"
	"fmt"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/nav"
)

// resolveTree picks the worktree a command should act on.
// An empty name means the tree containing the working directory;
// otherwise the accepted spellings are nav's exact tier, the
// same rule `wt go` applies, so the two can never drift apart.
func resolveTree(ctx context.Context, trees []gitx.Worktree, name string) (gitx.Worktree, error) {
	if name == "" {
		top, err := gitx.New("").TopLevel(ctx)
		if err != nil {
			return gitx.Worktree{}, err
		}
		for _, t := range trees {
			if t.Path == top {
				return t, nil
			}
		}
		return gitx.Worktree{}, fmt.Errorf("git does not list the current tree %s", top)
	}
	// Every tree is a candidate here, bare entries included:
	// exact-name commands may need to name states a jump never
	// targets.
	cands := make([]nav.Candidate, len(trees))
	for i, t := range trees {
		cands[i] = nav.Candidate{Branch: t.Branch, Path: t.Path}
	}
	if winner := nav.ResolveExact(cands, name); winner != nil {
		for _, t := range trees {
			if t.Path == winner.Path {
				return t, nil
			}
		}
	}
	return gitx.Worktree{}, errNoTreeMatches(name)
}

// errNoTreeMatches is the one spelling of the miss error, shared
// by exact resolution and fuzzy jumps.
func errNoTreeMatches(name string) error {
	return fmt.Errorf("no tree matches %q — `wt ls` shows what exists", name)
}

// checkBase is the one spelling of the unresolvable-base error,
// shared by new, claim, and resize.
func checkBase(ctx context.Context, g *gitx.Git, base string) error {
	if !g.HasCommit(ctx, base) {
		return preconditionf(
			"base %q does not resolve to a commit — fetch it, or set base in wt.toml", base)
	}
	return nil
}

// checkRemovable refuses trees git cannot remove cleanly, before
// any guard or sweep runs: a locked tree would fail only after wt
// had already deleted the planted copy files, and a prunable
// tree's directory is gone, so the guards (which run inside it)
// cannot vouch for anything — hand that cleanup to git. Shared by
// wt done and slot removal, the two tree-deleting paths.
func checkRemovable(t gitx.Worktree) error {
	if t.Locked {
		reason := ""
		if t.LockedReason != "" {
			reason = fmt.Sprintf(" (%s)", t.LockedReason)
		}
		return preconditionf("%s is locked%s — `git worktree unlock %s` first",
			t.Path, reason, t.Path)
	}
	if t.Prunable {
		return preconditionf(
			"%s is gone from disk — `git worktree prune` clears the stale registration",
			t.Path)
	}
	return nil
}

// treeHoldingBranch finds the worktree with branch checked out,
// for the R4 errors that must point straight at it.
func treeHoldingBranch(trees []gitx.Worktree, branch string) (gitx.Worktree, bool) {
	for _, t := range trees {
		if t.Branch == branch {
			return t, true
		}
	}
	return gitx.Worktree{}, false
}

// nameArg unpacks the optional [name] positional argument.
func nameArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
