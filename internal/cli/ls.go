package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/render"
	"github.com/loganthomas/wt/internal/repo"
	"github.com/loganthomas/wt/internal/state"
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
	ctx := cmd.Context()
	r, trees, err := repoTrees(ctx)
	if err != nil {
		return err
	}
	if porcelain {
		_, err := fmt.Fprint(cmd.OutOrStdout(), formatPorcelain(trees))
		return err
	}
	w, st := lsRepo(r)
	var tools toolsFunc
	if w != nil {
		tools = w.refreshTools(st)
	}
	facts := gatherTreeFacts(ctx, gitx.New(r.Root), trees, tools, time.Now())
	pooled := w != nil && w.cfg.Pool != nil
	if jsonOut {
		views := treeViews(trees, facts)
		if pooled {
			markSlots(views, slotViews(w, st, trees, facts))
		}
		return render.JSON(cmd.OutOrStdout(), views)
	}
	l := lookFor(cmd.OutOrStdout())
	out := formatRows(trees, facts, l)
	if pooled {
		out = formatSlots(poolRows(w, st, trees, facts), l)
	}
	if _, err := fmt.Fprint(cmd.OutOrStdout(), out); err != nil {
		return err
	}
	// A human staleness note on stderr, so stdout stays the machine
	// contract. Best-effort, and silent until wt has a fetch on
	// record, so it never touches the network or disturbs the listing.
	noteFetchStaleness(r, cmd.ErrOrStderr())
	return nil
}

// lsRepo loads what ls reads beyond git: the config, for pool mode
// and the refresh gate, and the state dir, for leases. ls must keep
// listing in a repo whose wt.toml is broken, so any failure returns
// nil and ls falls back to the plain tree table.
func lsRepo(r *repo.Repo) (*wtRepo, state.Dir) {
	cfg, err := loadMerged(r)
	if err != nil {
		return nil, ""
	}
	w := &wtRepo{repo: r, cfg: cfg}
	st, err := w.stateDir()
	if err != nil {
		return nil, ""
	}
	return w, st
}

// markSlots tags each slot tree in ls --json with its slot name and
// lease state. Unprovisioned slots have no tree, so no entry.
func markSlots(views []treeView, slots []slotView) {
	byPath := make(map[string]slotView, len(slots))
	for _, s := range slots {
		byPath[s.Path] = s
	}
	for i, v := range views {
		if s, ok := byPath[v.Path]; ok {
			views[i].Slot, views[i].Lease = s.Slot, s.State
		}
	}
}

// treeView is one worktree in ls --json: git's facts, spelled
// stably for machine consumers (D13).
type treeView struct {
	Branch         string     `json:"branch,omitempty"`
	Path           string     `json:"path"`
	Head           string     `json:"head,omitempty"`
	Bare           bool       `json:"bare,omitempty"`
	Detached       bool       `json:"detached,omitempty"`
	Locked         bool       `json:"locked,omitempty"`
	LockedReason   string     `json:"locked_reason,omitempty"`
	Prunable       bool       `json:"prunable,omitempty"`
	PrunableReason string     `json:"prunable_reason,omitempty"`
	Dirty          bool       `json:"dirty,omitempty"`
	CommittedAt    *time.Time `json:"committed_at,omitempty"` // HEAD's committer date
	Tools          string     `json:"tools,omitempty"`        // fresh | stale
	Slot           string     `json:"slot,omitempty"`         // pool mode only
	Lease          string     `json:"lease,omitempty"`        // free | claimed | stale
}

func treeViews(trees []gitx.Worktree, facts map[string]treeFacts) []treeView {
	views := make([]treeView, 0, len(trees))
	for _, t := range trees {
		f := facts[t.Path]
		views = append(views, treeView{
			Dirty:          f.dirty(),
			CommittedAt:    f.Committed,
			Tools:          f.Tools,
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

// formatRows renders the human tree table: a header, then one
// row per worktree with the path last, so a long path runs off
// the edge instead of pushing columns around. TOOLS appears only
// when some tree has a refresh gate to report on.
func formatRows(trees []gitx.Worktree, facts map[string]treeFacts, l look) string {
	tools := slices.ContainsFunc(trees, func(t gitx.Worktree) bool {
		return facts[t.Path].Tools != ""
	})
	titles := []string{"branch", "state", "head", "age", "path"}
	if tools {
		titles = slices.Insert(titles, 2, "tools")
	}
	rows := [][]string{l.header(titles...)}
	for _, t := range trees {
		f := facts[t.Path]
		state := treeState(t, f)
		row := []string{branchLabel(t), l.paint(treeStateStyle(t, f), state)}
		if tools {
			row = append(row, l.paint(toolsStyle(f.Tools), dash(f.Tools)))
		}
		rows = append(rows, append(row, shortHead(t.Head), dash(f.Age), l.path(t.Path)))
	}
	return render.Align(render.FitColumn(rows, len(titles)-1, l.width))
}

// treeState joins what's worth knowing about a working tree:
// clean or dirty first, then git's own locked/prunable marks.
// A prunable tree is gone from disk, so it has no clean or dirty.
func treeState(t gitx.Worktree, f treeFacts) string {
	var parts []string
	switch {
	case f.Dirty == nil:
	case *f.Dirty:
		parts = append(parts, "dirty")
	default:
		parts = append(parts, "clean")
	}
	if label := stateLabel(t); label != "" {
		parts = append(parts, label)
	}
	return dash(strings.Join(parts, ","))
}

// treeStateStyle flags a vanished tree as an error and anything
// blocking a clean `wt done` (edits, a lock) as a warning.
func treeStateStyle(t gitx.Worktree, f treeFacts) lipgloss.Style {
	switch {
	case t.Prunable:
		return styleBad
	case t.Locked || (f.Dirty != nil && *f.Dirty):
		return styleWarn
	}
	return lipgloss.NewStyle()
}

func toolsStyle(tools string) lipgloss.Style {
	if tools == "stale" {
		return styleWarn
	}
	return lipgloss.NewStyle()
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
