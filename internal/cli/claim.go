package cli

import (
	"cmp"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/pool"
)

func newClaimCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "claim <branch>",
		Short: "Claim a pool slot for a branch (plumbing)",
		Long: "Claim a free pool slot, reset it onto the base, and check out\n" +
			"the branch there — creating it off the base when it is new.\n" +
			"Prints the slot path on stdout; scripts and agents cd there.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClaim(cmd, args[0], base)
		},
	}
	cmd.Flags().StringVar(&base, "base", "",
		"ref to park and branch from (default: the configured base)")
	return cmd
}

func runClaim(cmd *cobra.Command, branch, baseFlag string) error {
	ctx := cmd.Context()
	p, err := openPool(ctx)
	if err != nil {
		return err
	}
	base := cmp.Or(baseFlag, p.cfg.Base)
	if !p.g.ValidBranchName(ctx, branch) {
		return usageError{fmt.Errorf("%q is not a valid branch name", branch)}
	}
	// R4 up front: claiming an already-checked-out branch would
	// only fail later, deep inside git, with a worse message.
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	if t, ok := treeHoldingBranch(trees, branch); ok {
		return preconditionf("branch %q is already checked out in %s", branch, t.Path)
	}
	if err := checkBase(ctx, p.g, base); err != nil {
		return err
	}
	dest, err := p.claimSlot(ctx, branch, base, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), dest)
	return nil
}

func newReleaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "release [name]",
		Short: "Release a pool slot, keeping its branch (plumbing)",
		Long: "Park the slot back on the base and free its lease.\n" +
			"The branch survives — handing its lifecycle to the PR flow —\n" +
			"where `wt done` would delete it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelease(cmd, nameArg(args))
		},
	}
}

func runRelease(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	p, err := openPool(ctx)
	if err != nil {
		return err
	}
	trees, err := p.g.Worktrees(ctx)
	if err != nil {
		return err
	}
	// A slot named directly can be leased yet have no worktree — a
	// claim or provision killed before worktree-add, or a tree
	// removed out of band. resolveTree cannot name such a slot, and
	// pool ls sends users here to clear exactly those states.
	if pool.IsSlotName(name) {
		if _, registered := findTree(trees, filepath.Join(p.treesDir(), name)); !registered {
			return p.releaseVacantSlot(name, cmd.ErrOrStderr())
		}
	}
	target, err := resolveTree(ctx, trees, name)
	if err != nil {
		return err
	}
	slot, ok := pool.SlotPath(p.treesDir(), target.Path)
	if !ok {
		return preconditionf(
			"%s is not a pool slot — `wt done` finishes personal trees", target.Path)
	}
	return p.releaseSlot(ctx, target, slot, false, cmd.ErrOrStderr())
}
