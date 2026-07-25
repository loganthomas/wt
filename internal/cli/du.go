// Per-tree disk usage for wt status (PLAN.md Phase 6, R10):
// parallel du runs, cached in the state dir so a 750k-file
// monorepo tree is walked at most once an hour.
package cli

import (
	"cmp"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/loganthomas/wt/internal/state"
)

// duCacheTTL bounds how stale a cached size may grow. Sizes move
// slowly and are read as a gauge, not an audit; an hour keeps
// repeated status calls instant on huge trees.
const duCacheTTL = time.Hour

// duFresh reports whether a cached measurement is still usable.
func duFresh(u state.DiskUsage, now time.Time) bool {
	return !u.MeasuredAt.IsZero() && now.Sub(u.MeasuredAt) <= duCacheTTL
}

// measureDiskUsage sizes each path, one du apiece, all concurrent:
// the trees are independent and the walks are I/O bound, so N
// sequential passes would make status crawl on exactly the repos
// pool mode targets. An unmeasurable path is omitted, never
// reported as a fake zero.
func measureDiskUsage(ctx context.Context, paths []string) map[string]int64 {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sizes := make(map[string]int64, len(paths))
	for _, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kb, err := duKB(ctx, path)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			sizes[path] = kb
		}()
	}
	wg.Wait()
	return sizes
}

// duKB runs `du -sk` on one path. -k is POSIX: the macOS/BSD and
// GNU du agree on it, where -h and friends diverge. du exits
// non-zero when any subdirectory is unreadable while still
// printing a valid total, so the exit status only matters when
// no total parsed: a partial size beats no size, and a tree with
// one sealed subdirectory must not re-walk on every status.
func duKB(ctx context.Context, path string) (int64, error) {
	out, err := exec.CommandContext(ctx, "du", "-sk", path).Output()
	field, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	kb, perr := strconv.ParseInt(field, 10, 64)
	if perr != nil {
		return 0, cmp.Or(err, perr)
	}
	return kb, nil
}
