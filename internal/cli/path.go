package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path [name]",
		Short: "Print a tree's absolute path (plumbing)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPath(cmd, nameArg(args))
		},
	}
}

func runPath(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	trees, err := repoTrees(ctx)
	if err != nil {
		return err
	}
	target, err := resolveTree(ctx, trees, name)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), target.Path)
	return nil
}
