package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

	// Guards before anything destructive (R2). The unpushed check
	// runs only when the branch is about to be deleted: with
	// --keep-branch every commit stays reachable through it.
	// Pristine copies of the configured copy files are wt's own
	// plantings and don't count as dirt; an edited one still does.
	pristine, err := pristineCopies(ctx, w.repo.Root, target.Path, w.cfg.Copy)
	if err != nil {
		return err
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

// pristineCopies returns the configured copy files wt may sweep
// aside on removal: untracked in the tree, and still matching the
// main checkout byte for byte. A tracked file belongs to git even
// if copy-listed, and a missing or edited copy is the user's data —
// both stay out of the sweep.
// Paths come back slash-separated to match git's status output.
func pristineCopies(
	ctx context.Context, srcRoot, treeRoot string, names []string,
) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	tracked, err := gitx.New(treeRoot).Tracked(ctx, names...)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range names {
		if tracked[filepath.ToSlash(name)] {
			continue
		}
		treeData, ok, err := readCopy(treeRoot, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		srcData, ok, err := readCopy(srcRoot, name)
		if err != nil {
			return nil, err
		}
		if ok && bytes.Equal(treeData, srcData) {
			out = append(out, filepath.ToSlash(name))
		}
	}
	return out, nil
}

// readCopy reads a copy-list file under root;
// ok is false when the file does not exist there.
func readCopy(root, name string) (data []byte, ok bool, err error) {
	data, err = os.ReadFile(filepath.Join(root, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("copy %s: %w", name, err)
	}
	return data, true, nil
}
