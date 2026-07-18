package cli

import (
	"bytes"
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
		RunE:  runLs,
	}
}

func runLs(cmd *cobra.Command, _ []string) error {
	trees, err := gitx.New("").Worktrees(cmd.Context())
	if err != nil {
		return err
	}
	rows, err := formatRows(trees)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), rows)
	return err
}

// formatRows renders one aligned row per worktree.
// Every row carries the trailing state cell:
// mixing two- and three-cell rows would split tabwriter's column block
// and misalign the state column.
// The padding this leaves after empty state cells is trimmed,
// since stdout must stay free of trailing whitespace
// for machine consumers (D13).
func formatRows(trees []gitx.Worktree) (string, error) {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 2, 0, 2, ' ', 0)
	for _, t := range trees {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", branchLabel(t), t.Path, stateLabel(t))
	}
	if err := tw.Flush(); err != nil {
		return "", err
	}
	var out strings.Builder
	for line := range strings.Lines(buf.String()) {
		out.WriteString(strings.TrimRight(line, " \n"))
		out.WriteByte('\n')
	}
	return out.String(), nil
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
