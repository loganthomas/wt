package cli

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/loganthomas/wt/internal/freshness"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/pool"
	"github.com/loganthomas/wt/internal/state"
)

// treeFacts are the per-tree table columns that cost git calls,
// gathered once per listing so every table reads the same answer.
// Zero values mean "unknown" and render as "-".
type treeFacts struct {
	Dirty     *bool
	Committed *time.Time
	Age       string // HEAD commit age, compact ("4d")
	Tools     string // "fresh" | "stale"; empty when no refresh gate applies
}

// dirty is the machine-contract reading: true only when known
// dirty, so JSON can omit the field otherwise.
func (f treeFacts) dirty() bool {
	return f.Dirty != nil && *f.Dirty
}

// toolsFunc reports a tree's refresh-gate state, or "" when none
// applies; nil when the config could not be loaded.
type toolsFunc func(path string) string

// gatherTreeFacts runs one git status per tree, all concurrent
// (the trees are independent and each status is I/O bound), plus
// a single git show for every HEAD's commit time. Bare and
// prunable trees have no working tree to ask about.
func gatherTreeFacts(
	ctx context.Context, g *gitx.Git, trees []gitx.Worktree, tools toolsFunc, now time.Time,
) map[string]treeFacts {
	var live []gitx.Worktree
	var heads []string
	for _, t := range trees {
		if t.Bare || t.Prunable {
			continue
		}
		live = append(live, t)
		if t.Head != "" {
			heads = append(heads, t.Head)
		}
	}
	committed, err := g.CommitTimes(ctx, heads)
	if err != nil {
		committed = nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	facts := make(map[string]treeFacts, len(live))
	for _, t := range live {
		wg.Go(func() {
			f := treeFacts{}
			if dirty, err := gitx.New(t.Path).IsDirty(ctx); err == nil {
				f.Dirty = &dirty
			}
			if at, ok := committed[t.Head]; ok {
				f.Committed = &at
				f.Age = compactAge(now.Sub(at))
			}
			if tools != nil {
				f.Tools = tools(t.Path)
			}
			mu.Lock()
			defer mu.Unlock()
			facts[t.Path] = f
		})
	}
	wg.Wait()
	return facts
}

// compactAge is freshness.Age for table cells: "4d", not "4d ago",
// and "now" for anything under a minute.
func compactAge(d time.Duration) string {
	if d < time.Minute {
		return "now"
	}
	return strings.TrimSuffix(freshness.Age(d), " ago")
}

// refreshTools compares a managed tree's recorded refresh hash with
// its lockfiles now: "stale" means the next claim or new reruns
// the refresh hook. Only a configured gate gives the column
// meaning; without one, every tree reads "".
func (w *wtRepo) refreshTools(st state.Dir) toolsFunc {
	files := w.cfg.Hooks.RefreshIfChanged
	if w.cfg.Hooks.Refresh == "" || len(files) == 0 {
		return nil
	}
	return func(path string) string {
		name, ok := w.treeStateName(path)
		if !ok {
			return ""
		}
		current, err := pool.Hash(path, files)
		if err != nil {
			return ""
		}
		if current == st.RefreshHash(name) {
			return "fresh"
		}
		return "stale"
	}
}
