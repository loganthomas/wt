package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/repo"
)

// resolveTree picks the worktree a command should act on.
// An empty name means the tree containing the working directory;
// otherwise a tree matches by branch name, sanitized branch name,
// or directory basename — the three spellings a user might reach for.
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
	// Branch matches win over directory names: when one tree's
	// directory happens to carry another tree's branch name,
	// the user almost certainly means the branch.
	for _, t := range trees {
		if t.Branch != "" && (t.Branch == name || repo.SanitizeBranch(t.Branch) == name) {
			return t, nil
		}
	}
	for _, t := range trees {
		if filepath.Base(t.Path) == name {
			return t, nil
		}
	}
	return gitx.Worktree{}, fmt.Errorf("no tree matches %q — `wt ls` shows what exists", name)
}

// nameArg unpacks the optional [name] positional argument.
func nameArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
