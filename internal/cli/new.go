package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/repo"
)

func newNewCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "new <branch>",
		Short: "Create a worktree on a new branch off the base",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNew(cmd, args[0], base)
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "ref to branch from (default: the configured base)")
	return cmd
}

func runNew(cmd *cobra.Command, branch, baseFlag string) error {
	ctx := cmd.Context()
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	g := gitx.New(w.repo.Root)
	base := cmp.Or(baseFlag, w.cfg.Base)

	if !g.ValidBranchName(ctx, branch) {
		return usageError{fmt.Errorf("%q is not a valid branch name", branch)}
	}
	if g.HasCommit(ctx, "refs/heads/"+branch) {
		trees, err := g.Worktrees(ctx)
		if err != nil {
			return err
		}
		// R4: when the branch lives in some tree already,
		// the error must point straight at it.
		for _, t := range trees {
			if t.Branch == branch {
				return preconditionf("branch %q is already checked out in %s", branch, t.Path)
			}
		}
		return preconditionf(
			"branch %q already exists — pick another name, or delete the branch first", branch)
	}
	if !g.HasCommit(ctx, base) {
		return preconditionf(
			"base %q does not resolve to a commit — fetch it, or set base in wt.toml", base)
	}

	dest := filepath.Join(w.treesDir(), repo.SanitizeBranch(branch))
	if _, err := os.Stat(dest); err == nil {
		return preconditionf(
			"%s already exists (branch names flatten '/' to '-') — pick another name", dest)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := g.WorktreeAdd(ctx, dest, branch, base); err != nil {
		return err
	}
	chatter := cmd.ErrOrStderr()
	fmt.Fprintf(chatter, "created %s (branch %s off %s)\n", dest, branch, base)

	if err := copyFiles(w.repo.Root, dest, w.cfg.Copy, chatter); err != nil {
		return err
	}
	if setup := w.cfg.Hooks.Setup; setup != "" {
		fmt.Fprintf(chatter, "running setup hook: %s\n", setup)
		if err := runHook(ctx, dest, setup, chatter); err != nil {
			return fmt.Errorf("setup hook failed: %w — the tree remains at %s", err, dest)
		}
	}

	// The tree path is the machine-facing product (D13);
	// the Phase 3 shim will cd here.
	fmt.Fprintln(cmd.OutOrStdout(), dest)
	return nil
}

// copyFiles ports the configured untracked files from the main
// checkout into the fresh tree. Copies, never symlinks: symlinked
// configs break tools that resolve paths through them (D5).
// A missing source is a note, not an error — .env may simply not
// exist on this machine.
func copyFiles(srcRoot, dstRoot string, names []string, chatter io.Writer) error {
	for _, name := range names {
		err := copyFile(filepath.Join(srcRoot, name), filepath.Join(dstRoot, name))
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(chatter, "copy: %s not found in the main checkout, skipped\n", name)
			continue
		}
		if err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
		fmt.Fprintf(chatter, "copy: %s\n", name)
	}
	return nil
}

// copyFile copies one file, creating parent directories and
// carrying the source permissions over — copy sources are often
// secrets (.env) deliberately locked down.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// runHook runs a user hook command inside dir through sh.
// Both hook streams land on wt's stderr: stdout stays reserved
// for wt's own machine output (D13).
func runHook(ctx context.Context, dir, command string, chatter io.Writer) error {
	hook := exec.CommandContext(ctx, "sh", "-c", command)
	hook.Dir = dir
	hook.Stdout = chatter
	hook.Stderr = chatter
	return hook.Run()
}
