package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/guard"
	"github.com/loganthomas/wt/internal/pool"
)

func newDoneCmd() *cobra.Command {
	var keepBranch bool
	cmd := &cobra.Command{
		Use:     "done [name]",
		Aliases: []string{"rm"},
		Short:   "Finish a tree: safety checks, then remove it and its branch",
		Args:    cobra.MaximumNArgs(1),
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
	// A slot is finished by releasing it, never by removing it:
	// the warm tree is the pool's whole value. Everything below
	// stays the personal-tree path.
	if w.cfg.Pool != nil {
		if slot, ok := pool.SlotPath(w.treesDir(), target.Path); ok {
			p, err := poolOf(w)
			if err != nil {
				return err
			}
			return p.releaseSlot(ctx, target, slot, !keepBranch, cmd.ErrOrStderr())
		}
	}
	// Checked before the guards and the copy sweep: git would
	// refuse the removal anyway, but only after wt had already
	// deleted the planted copy files.
	if target.Locked {
		reason := ""
		if target.LockedReason != "" {
			reason = fmt.Sprintf(" (%s)", target.LockedReason)
		}
		return preconditionf("%s is locked%s — `git worktree unlock %s` first",
			target.Path, reason, target.Path)
	}
	// A prunable tree's directory is already gone, so the guards
	// (which run inside it) cannot vouch for anything; hand the
	// cleanup to git rather than fail on a raw chdir error.
	if target.Prunable {
		return preconditionf(
			"%s is gone from disk — `git worktree prune` clears the stale registration",
			target.Path)
	}

	// Guards before anything destructive (R2). The unpushed check
	// runs only when the branch is about to be deleted: with
	// --keep-branch every commit stays reachable through it.
	// Pristine copies of the configured copy files are wt's own
	// plantings and don't count as dirt; an edited one still does.
	pristine, edited, err := splitCopies(ctx, w.repo.Root, target.Path, w.cfg.Copy)
	if err != nil {
		return err
	}
	// Edited copies are refused here, not left to the dirty guard:
	// copy files are routinely gitignored, invisible to git status,
	// and `git worktree remove` deletes ignored files without asking.
	if len(edited) > 0 {
		return preconditionf(
			"%s: the planted copy %s no longer matches the main checkout — "+
				"back it up, or make the two match first",
			target.Path, edited[0])
	}
	if err := guard.CheckDirty(ctx, target.Path, pristine...); err != nil {
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

	// The pristine copies go first: git itself refuses to remove
	// a tree holding untracked files, and these are wt's to clean.
	for _, name := range pristine {
		if err := os.Remove(filepath.Join(target.Path, name)); err != nil {
			return err
		}
	}
	if err := g.WorktreeRemove(ctx, target.Path); err != nil {
		return err
	}
	st, err := w.stateDir()
	if err != nil {
		return err
	}
	if err := st.RemoveTree(filepath.Base(target.Path)); err != nil {
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
