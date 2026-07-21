package cli

import (
	"cmp"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
)

func newLsCmd() *cobra.Command {
	var porcelain bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLs(cmd, porcelain)
		},
	}
	cmd.Flags().BoolVar(&porcelain, "porcelain", false,
		"stable tab-separated output for scripts")
	return cmd
}

func runLs(cmd *cobra.Command, porcelain bool) error {
	_, trees, err := repoTrees(cmd.Context())
	if err != nil {
		return err
	}
	format := formatRows
	if porcelain {
		format = formatPorcelain
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), format(trees))
	return err
}

// formatPorcelain renders the stable machine format:
// one line per tree, three tab-separated fields
// (branch label, path, comma-joined states).
// An empty state becomes "-" so the field count never varies
// and awk/cut consumers can rely on positions (D13).
func formatPorcelain(trees []gitx.Worktree) string {
	var out strings.Builder
	for _, t := range trees {
		fmt.Fprintf(&out, "%s\t%s\t%s\n", branchLabel(t), t.Path, cmp.Or(stateLabel(t), "-"))
	}
	return out.String()
}

// formatRows renders one aligned row per worktree.
func formatRows(trees []gitx.Worktree) string {
	rows := make([][]string, 0, len(trees))
	for _, t := range trees {
		rows = append(rows, []string{branchLabel(t), t.Path, stateLabel(t)})
	}
	return alignRows(rows)
}

// alignRows renders rows in aligned columns, shared by every
// tabular listing. Widths are computed by hand rather than with
// text/tabwriter: padding must only ever sit between cells,
// because trimming rendered lines would also strip a path's own
// trailing spaces, and stdout must stay exact for machine
// consumers (D13). Trailing empty cells drop their padding too,
// so no line ever ends in spaces.
func alignRows(rows [][]string) string {
	var width []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(width) {
				width = append(width, 0)
			}
			width[i] = max(width[i], utf8.RuneCountInString(cell))
		}
	}
	const gap = 2
	var out strings.Builder
	for _, row := range rows {
		last := len(row) - 1
		for last > 0 && row[last] == "" {
			last--
		}
		for i := range last {
			// fmt pads %s to a minimum rune count, matching the width math above.
			fmt.Fprintf(&out, "%-*s", width[i]+gap, row[i])
		}
		fmt.Fprintln(&out, row[last])
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
