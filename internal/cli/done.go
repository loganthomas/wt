package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/guard"
)

func newDoneCmd() *cobra.Command {
	var keepBranch bool
	cmd := &cobra.Command{
		Use:     "done [name]",
		Aliases: []string{"rm"},
		Short:   "Finish a tree: safety checks, then remove it and its branch",
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDone(cmd, nameArg(args), keepBranch)
		},
	}
	cmd.Flags().BoolVar(&keepBranch, "keep-branch", false, "remove the tree but keep its branch")
	return cmd
}

func runDone(cmd *cobra.Command, name string, keepBranch bool) error {
	ctx := cmd.Context()
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	g := gitx.New(w.repo.Root)
	trees, err := g.Worktrees(ctx)
	if err != nil {
		return err
	}
	target, err := resolveTree(ctx, trees, name)
	if err != nil {
		return err
	}
	if target.Path == w.repo.Root {
		return preconditionf("%s is the main checkout — wt only removes trees it manages", target.Path)
	}

	// Guards before anything destructive (R2). The unpushed check
	// runs only when the branch is about to be deleted: with
	// --keep-branch every commit stays reachable through it.
	if err := guard.CheckDirty(ctx, target.Path); err != nil {
		return err
	}
	if target.Detached {
		if err := guard.CheckOrphans(ctx, target.Path); err != nil {
			return err
		}
	}
	deleteBranch := target.Branch != "" && !keepBranch
	if deleteBranch {
		if err := guard.CheckUnpushed(ctx, target.Path, w.cfg.Base); err != nil {
			return err
		}
	}

	if err := g.WorktreeRemove(ctx, target.Path); err != nil {
		return err
	}
	chatter := cmd.ErrOrStderr()
	fmt.Fprintf(chatter, "removed %s\n", target.Path)
	if deleteBranch {
		if err := g.DeleteBranch(ctx, target.Branch); err != nil {
			return err
		}
		fmt.Fprintf(chatter, "deleted branch %s\n", target.Branch)
	} else if target.Branch != "" {
		fmt.Fprintf(chatter, "kept branch %s\n", target.Branch)
	}
	return nil
}
