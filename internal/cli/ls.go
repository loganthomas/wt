package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
)

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List worktrees",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLs(cmd)
		},
	}
}

func runLs(cmd *cobra.Command) error {
	trees, err := gitx.New("").Worktrees(cmd.Context())
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 0, 2, ' ', 0)
	for _, t := range trees {
		// The state cell is omitted when empty: tabwriter pads every
		// tab-terminated cell, and stdout must stay free of trailing
		// whitespace for machine consumers (D13).
		if state := stateLabel(t); state != "" {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", branchLabel(t), t.Path, state)
		} else {
			fmt.Fprintf(tw, "%s\t%s\n", branchLabel(t), t.Path)
		}
	}
	return tw.Flush()
}

func branchLabel(t gitx.Worktree) string {
	switch {
	case t.Bare:
		return "(bare)"
	case t.Detached:
		return "(detached)"
	default:
		return t.Branch
	}
}

func stateLabel(t gitx.Worktree) string {
	var states []string
	if t.Locked {
		states = append(states, "locked")
	}
	if t.Prunable {
		states = append(states, "prunable")
	}
	return strings.Join(states, ",")
}
