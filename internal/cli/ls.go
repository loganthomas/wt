package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
)

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List worktrees",
		Args:  cobra.NoArgs,
		RunE:  runLs,
	}
}

func runLs(cmd *cobra.Command, _ []string) error {
	trees, err := repoTrees(cmd.Context())
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), formatRows(trees))
	return err
}

// formatRows renders one aligned row per worktree.
// Widths are computed by hand rather than with text/tabwriter:
// padding must only ever sit between cells,
// because trimming rendered lines would also strip
// a path's own trailing spaces,
// and stdout must stay exact for machine consumers (D13).
func formatRows(trees []gitx.Worktree) string {
	branchWidth, pathWidth := 0, 0
	rows := make([][3]string, 0, len(trees))
	for _, t := range trees {
		row := [3]string{branchLabel(t), t.Path, stateLabel(t)}
		rows = append(rows, row)
		branchWidth = max(branchWidth, utf8.RuneCountInString(row[0]))
		pathWidth = max(pathWidth, utf8.RuneCountInString(row[1]))
	}
	const gap = 2
	var out strings.Builder
	for _, row := range rows {
		// fmt pads %s to a minimum rune count, matching the width math above.
		if row[2] == "" {
			fmt.Fprintf(&out, "%-*s%s\n", branchWidth+gap, row[0], row[1])
		} else {
			fmt.Fprintf(&out, "%-*s%-*s%s\n", branchWidth+gap, row[0], pathWidth+gap, row[1], row[2])
		}
	}
	return out.String()
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
