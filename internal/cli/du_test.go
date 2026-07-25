package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loganthomas/wt/internal/gittest"
	"github.com/loganthomas/wt/internal/state"
)

func TestMeasureDiskUsageSizesEveryPath(t *testing.T) {
	a, b := gittest.TempDir(t), gittest.TempDir(t)
	gittest.WriteFile(t, a, "f.txt", "hello\n")
	sizes := measureDiskUsage(t.Context(), []string{a, b, "/no/such/path"})
	if kb, ok := sizes[a]; !ok || kb <= 0 {
		t.Errorf("sizes[%q] = %d, %v; want a positive size", a, kb, ok)
	}
	if _, ok := sizes[b]; !ok {
		t.Errorf("sizes[%q] missing; want an entry for an empty dir", b)
	}
	// An unmeasurable path is omitted, never reported as zero.
	if kb, ok := sizes["/no/such/path"]; ok {
		t.Errorf("sizes[missing path] = %d, want no entry", kb)
	}
}

// du exits non-zero when a subdirectory is unreadable while still
// printing a valid total; a partial size beats no size, and the
// tree must not fall out of the cache and re-walk every run.
func TestMeasureDiskUsageKeepsPartialTotals(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything; the permission failure cannot be staged")
	}
	dir := gittest.TempDir(t)
	gittest.WriteFile(t, dir, "readable.txt", "hello\n")
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	sizes := measureDiskUsage(t.Context(), []string{dir})
	if kb, ok := sizes[dir]; !ok || kb < 0 {
		t.Errorf("sizes[%q] = %d, %v; want du's partial total despite the exit status",
			dir, kb, ok)
	}
}

func TestDuCacheFreshness(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		u    state.DiskUsage
		want bool
	}{
		{"just measured", state.DiskUsage{MeasuredAt: now}, true},
		{"within the window", state.DiskUsage{MeasuredAt: now.Add(-duCacheTTL / 2)}, true},
		{"aged out", state.DiskUsage{MeasuredAt: now.Add(-duCacheTTL - time.Minute)}, false},
		{"never measured", state.DiskUsage{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := duFresh(tt.u, now); got != tt.want {
				t.Errorf("duFresh(%+v) = %v, want %v", tt.u, got, tt.want)
			}
		})
	}
}
