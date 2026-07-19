// Package guard holds the safety checks that run before any
// destructive wt command. They exist before the commands do
// (PLAN.md R2): a tree is only ever removed or reset after every
// guard here reports it safe.
package guard

import (
	"context"
	"fmt"
	"slices"

	"github.com/loganthomas/wt/internal/gitx"
)

// Error is a failed safety check. It maps to exit code 3
// (precondition failed, D13) and always tells the user how to
// proceed, not just what blocked them.
type Error struct {
	Tree   string // worktree path the check ran in
	Reason string // what blocks the operation
	Hint   string // the way forward
}

func (v *Error) Error() string {
	return fmt.Sprintf("%s: %s — %s", v.Tree, v.Reason, v.Hint)
}

// ExitCode maps to D13's "precondition failed" contract code.
func (v *Error) ExitCode() int { return 3 }

// CheckDirty reports an Error when the tree has uncommitted
// changes — staged, unstaged, or untracked. All three would be
// destroyed by a worktree remove.
// Untracked files named in tolerateUntracked are ignored:
// wt plants its configured copy files in every tree,
// and files it planted must not block leaving.
func CheckDirty(ctx context.Context, tree string, tolerateUntracked ...string) error {
	entries, err := gitx.New(tree).Status(ctx)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Code == "??" && slices.Contains(tolerateUntracked, e.Path) {
			continue
		}
		return &Error{
			Tree:   tree,
			Reason: "the tree has uncommitted changes",
			Hint:   "commit, stash, or discard them first",
		}
	}
	return nil
}

// CheckUnpushed reports an Error when HEAD carries commits
// reachable from neither any remote-tracking branch nor the local
// base branch. Such commits would become unreachable if the
// branch were deleted. A base that doesn't resolve is skipped,
// so repos without the configured base still get the remote check.
func CheckUnpushed(ctx context.Context, tree, base string) error {
	g := gitx.New(tree)
	spec := []string{"HEAD", "--not", "--remotes"}
	if base != "" && g.HasCommit(ctx, base) {
		spec = append(spec, base)
	}
	n, err := g.CommitCount(ctx, spec...)
	if err != nil {
		return err
	}
	if n > 0 {
		return &Error{
			Tree:   tree,
			Reason: fmt.Sprintf("%s neither pushed nor merged into %q", countCommits(n), base),
			Hint:   "push or merge the branch first, or keep it with --keep-branch",
		}
	}
	return nil
}

// CheckOrphans reports an Error when the tree's detached HEAD
// carries commits no branch or tag can reach — the one state
// where removing a tree silently discards work (R2).
func CheckOrphans(ctx context.Context, tree string) error {
	g := gitx.New(tree)
	// Not `--not --all`: git's --all includes HEAD itself,
	// which would negate the very commits being looked for.
	n, err := g.CommitCount(ctx, "HEAD", "--not", "--branches", "--tags", "--remotes")
	if err != nil {
		return err
	}
	if n > 0 {
		return &Error{
			Tree:   tree,
			Reason: fmt.Sprintf("%s reachable only from its detached HEAD", countCommits(n)),
			Hint:   "rescue them with `git branch <name> HEAD` there first",
		}
	}
	return nil
}

func countCommits(n int) string {
	if n == 1 {
		return "the tree has 1 commit"
	}
	return fmt.Sprintf("the tree has %d commits", n)
}
