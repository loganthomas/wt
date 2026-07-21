package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
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
		if t, ok := treeHoldingBranch(trees, branch); ok {
			return preconditionf("branch %q is already checked out in %s", branch, t.Path)
		}
		return preconditionf(
			"branch %q already exists — pick another name, or delete the branch first", branch)
	}
	if err := checkBase(ctx, g, base); err != nil {
		return err
	}
	chatter := cmd.ErrOrStderr()

	// Pool mode: the same intent lands in a claimed slot instead
	// of a fresh tree (D3 — the [pool] table is the dispatch).
	if w.cfg.Pool != nil {
		p, err := poolOf(w)
		if err != nil {
			return err
		}
		dest, err := p.claimSlot(ctx, branch, base, chatter)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), dest)
		return nil
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
	fmt.Fprintf(chatter, "created %s (branch %s off %s)\n", dest, branch, base)

	if err := copyFiles(ctx, w.repo.Root, dest, w.cfg.Copy, chatter); err != nil {
		return fmt.Errorf("%w — the tree remains at %s", err, dest)
	}
	st, err := w.stateDir()
	if err != nil {
		return err
	}
	if err := finishFresh(ctx, w.cfg, st, dest, filepath.Base(dest), chatter); err != nil {
		return fmt.Errorf("%w — the tree remains at %s", err, dest)
	}

	// The tree path is the machine-facing product (D13);
	// the Phase 3 shim will cd here.
	fmt.Fprintln(cmd.OutOrStdout(), dest)
	return nil
}

// runHook runs a user hook command inside dir through sh.
// Both hook streams land on wt's stderr: stdout stays reserved
// for wt's own machine output (D13).
// The scrubbed environment keeps a wrapping git hook's GIT_DIR
// and kin from retargeting any git the hook itself runs.
func runHook(ctx context.Context, dir, command string, chatter io.Writer) error {
	hook := exec.CommandContext(ctx, "sh", "-c", command)
	hook.Dir = dir
	hook.Env = gitx.ScrubbedEnv()
	hook.Stdout = chatter
	hook.Stderr = chatter
	return hook.Run()
}
