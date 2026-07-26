// wt status (PLAN.md Phase 6): the repo overview — mode, base
// freshness, per-tree disk usage, slot occupancy. One view value
// feeds the human table and --json both, so the two cannot drift
// (D13).
package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/freshness"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/render"
	"github.com/loganthomas/wt/internal/state"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Repo overview: mode, base freshness, trees, slots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	return cmd
}

// statusView is the whole overview; render.JSON emits it verbatim,
// formatStatus lays the same fields out for humans.
type statusView struct {
	Mode  string       `json:"mode"` // default | pool
	Base  baseView     `json:"base"`
	Trees []treeStatus `json:"trees"`
	Pool  *poolStatus  `json:"pool,omitempty"`
}

type baseView struct {
	Name      string     `json:"name"`
	LastFetch *time.Time `json:"last_fetch,omitempty"` // absent: never fetched by wt
	Stale     bool       `json:"stale"`
}

type treeStatus struct {
	Branch   string `json:"branch,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Bare     bool   `json:"bare,omitempty"`
	Path     string `json:"path"`
	DiskKB   *int64 `json:"disk_kb,omitempty"` // absent: not measurable
}

type poolStatus struct {
	Size  int        `json:"size"`
	Slots []slotView `json:"slots"`
}

func runStatus(cmd *cobra.Command, jsonOut bool) error {
	ctx := cmd.Context()
	w, err := openRepo(ctx)
	if err != nil {
		return err
	}
	trees, err := gitx.New(w.repo.Root).Worktrees(ctx)
	if err != nil {
		return err
	}
	st, err := w.stateDir()
	if err != nil {
		return err
	}
	view := buildStatusView(ctx, w, st, trees, time.Now())
	if jsonOut {
		return render.JSON(cmd.OutOrStdout(), view)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), formatStatus(view))
	return err
}

func buildStatusView(
	ctx context.Context, w *wtRepo, st state.Dir, trees []gitx.Worktree, now time.Time,
) statusView {
	view := statusView{
		Mode: "default",
		Base: baseFreshness(w.cfg.Base, w.cfg.StalenessWindow(), st, now),
	}
	sizes := treeSizes(ctx, w, st, trees, now)
	for _, t := range trees {
		ts := treeStatus{
			Branch: t.Branch, Detached: t.Detached, Bare: t.Bare, Path: t.Path,
		}
		if kb, ok := sizes[t.Path]; ok {
			ts.DiskKB = &kb
		}
		view.Trees = append(view.Trees, ts)
	}
	if w.cfg.Pool != nil {
		view.Mode = "pool"
		view.Pool = &poolStatus{Size: w.cfg.Pool.Size, Slots: slotViews(w, st, trees)}
	}
	return view
}

// baseFreshness reads the base's fetch record without touching
// the network (D7): status is a read command.
func baseFreshness(base string, window int, st state.Dir, now time.Time) baseView {
	v := baseView{Name: base}
	last, ok := st.LastFetch()
	v.Stale = freshness.Stale(last, window, now)
	if ok {
		v.LastFetch = &last
	}
	return v
}

// treeSizes maps tree paths to sizes, serving cached measurements
// while they are fresh and re-measuring the rest in one parallel
// pass. Only wt-managed trees and the main checkout have cache
// homes in the state dir; a hand-made tree elsewhere is simply
// measured every time.
func treeSizes(
	ctx context.Context, w *wtRepo, st state.Dir, trees []gitx.Worktree, now time.Time,
) map[string]int64 {
	sizes := make(map[string]int64, len(trees))
	var measure []string
	for _, t := range trees {
		if u, ok := cachedSize(w, st, t.Path); ok && duFresh(u, now) {
			sizes[t.Path] = u.KB
			continue
		}
		measure = append(measure, t.Path)
	}
	for path, kb := range measureDiskUsage(ctx, measure) {
		sizes[path] = kb
		// Best-effort: a cache that fails to write only costs a
		// re-measure next time.
		writeCachedSize(w, st, path, state.DiskUsage{KB: kb, MeasuredAt: now})
	}
	return sizes
}

func cachedSize(w *wtRepo, st state.Dir, path string) (state.DiskUsage, bool) {
	if path == w.repo.Root {
		return st.RootDiskUsage()
	}
	if name, ok := w.treeStateName(path); ok {
		return st.TreeDiskUsage(name)
	}
	return state.DiskUsage{}, false
}

func writeCachedSize(w *wtRepo, st state.Dir, path string, u state.DiskUsage) {
	if path == w.repo.Root {
		_ = st.WriteRootDiskUsage(u)
		return
	}
	if name, ok := w.treeStateName(path); ok {
		_ = st.WriteTreeDiskUsage(name, u)
	}
}

// formatStatus lays the view out for humans: a mode/base header,
// one sized row per tree, and, in pool mode, one row per slot in
// exactly the wt pool ls spelling.
func formatStatus(view statusView) string {
	mode := view.Mode
	if view.Pool != nil {
		mode = fmt.Sprintf("pool (%d %s)", view.Pool.Size, plural(view.Pool.Size, "slot"))
	}
	out := render.Align([][]string{
		{"mode", mode},
		{"base", baseLine(view.Base)},
	})

	rows := make([][]string, 0, len(view.Trees))
	for _, t := range view.Trees {
		size := "?"
		if t.DiskKB != nil {
			size = humanKB(*t.DiskKB)
		}
		rows = append(rows, []string{worktreeLabel(t.Bare, t.Detached, t.Branch), t.Path, size})
	}
	if len(rows) > 0 {
		out += "\n" + render.Align(rows)
	}

	if view.Pool == nil {
		return out
	}
	slotRows := make([][]string, 0, len(view.Pool.Slots))
	for _, s := range view.Pool.Slots {
		slotRows = append(slotRows, slotRow(s))
	}
	return out + "\n" + render.Align(slotRows)
}

// baseLine phrases the base's fetch age through lastFetchPhrase,
// the same spelling the opportunistic-fetch notice uses, so the
// two cannot drift on how an age reads.
func baseLine(b baseView) string {
	var last time.Time
	if b.LastFetch != nil {
		last = *b.LastFetch
	}
	line := b.Name + " — " + lastFetchPhrase(last)
	if b.Stale && b.LastFetch != nil {
		line += " (stale — `wt sync` to refresh)"
	}
	return line
}

// humanKB renders a kilobyte count at gauge precision: sizes are
// for spotting the tree eating the disk, not for accounting.
func humanKB(kb int64) string {
	switch {
	case kb < 1024:
		return fmt.Sprintf("%dK", kb)
	case kb < 1024*1024:
		return fmt.Sprintf("%.1fM", float64(kb)/1024)
	default:
		return fmt.Sprintf("%.1fG", float64(kb)/(1024*1024))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
