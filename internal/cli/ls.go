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
	var porcelain, jsonOut bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLs(cmd, porcelain, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&porcelain, "porcelain", false,
		"stable tab-separated output for scripts")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}

func runLs(cmd *cobra.Command, porcelain, jsonOut bool) error {
	if porcelain && jsonOut {
		return usageError{fmt.Errorf("--porcelain and --json are two spellings " +
			"of the machine listing — choose one")}
	}
	r, trees, err := repoTrees(cmd.Context())
	if err != nil {
		return err
	}
	if jsonOut {
		return render.JSON(cmd.OutOrStdout(), treeViews(trees))
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

// treeView is one worktree in ls --json: git's facts, spelled
// stably for machine consumers (D13).
type treeView struct {
	Branch         string `json:"branch,omitempty"`
	Path           string `json:"path"`
	Head           string `json:"head,omitempty"`
	Bare           bool   `json:"bare,omitempty"`
	Detached       bool   `json:"detached,omitempty"`
	Locked         bool   `json:"locked,omitempty"`
	LockedReason   string `json:"locked_reason,omitempty"`
	Prunable       bool   `json:"prunable,omitempty"`
	PrunableReason string `json:"prunable_reason,omitempty"`
}

func treeViews(trees []gitx.Worktree) []treeView {
	views := make([]treeView, 0, len(trees))
	for _, t := range trees {
		views = append(views, treeView{
			Branch:         t.Branch,
			Path:           t.Path,
			Head:           t.Head,
			Bare:           t.Bare,
			Detached:       t.Detached,
			Locked:         t.Locked,
			LockedReason:   t.LockedReason,
			Prunable:       t.Prunable,
			PrunableReason: t.PrunableReason,
		})
	}
	return views
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
	return worktreeLabel(t.Bare, t.Detached, t.Branch)
}

// worktreeLabel is the one spelling of a tree's branch cell,
// shared by wt ls (plain and porcelain) and wt status so the two
// can never label the same tree differently (D13).
func worktreeLabel(bare, detached bool, branch string) string {
	switch {
	case bare:
		return "(bare)"
	case detached:
		return "(detached)"
	default:
		return branch
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
