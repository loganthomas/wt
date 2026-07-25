package cli

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/render"
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
	r, trees, err := repoTrees(cmd.Context())
	if err != nil {
		return err
	}
	format := formatRows
	if porcelain {
		format = formatPorcelain
	}
	if _, err := fmt.Fprint(cmd.OutOrStdout(), format(trees)); err != nil {
		return err
	}
	// A human staleness note on stderr, so stdout stays the machine
	// contract (D13). Porcelain callers are scripts: no chatter for
	// them. Best-effort, and silent until wt has a fetch on record,
	// so it never touches the network or disturbs the listing.
	if !porcelain {
		noteFetchStaleness(r, cmd.ErrOrStderr())
	}
	return nil
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
	return render.Align(rows)
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
