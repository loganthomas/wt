package state_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/loganthomas/wt/internal/state"
)

func TestLastFetchRoundTrip(t *testing.T) {
	d := state.Dir(t.TempDir())

	if got, ok := d.LastFetch(); ok {
		t.Errorf("LastFetch on fresh state = (%v, true), want (_, false)", got)
	}

	// Sub-second precision is dropped: the stamp round-trips through
	// RFC3339 seconds, which is all a 24h staleness window needs.
	want := time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC)
	if err := d.WriteLastFetch(want); err != nil {
		t.Fatal(err)
	}
	got, ok := d.LastFetch()
	if !ok {
		t.Fatal("LastFetch after write = (_, false), want (_, true)")
	}
	if !got.Equal(want) {
		t.Errorf("LastFetch = %v, want %v", got, want)
	}

	// The on-disk location is part of the documented state layout
	// (PLAN.md, State layout): last_fetch at the state root.
	onDisk := filepath.Join(string(d), "last_fetch")
	if _, err := os.Stat(onDisk); err != nil {
		t.Errorf("timestamp not at the documented layout path: %v", err)
	}
}

func TestLastFetchIgnoresGarbage(t *testing.T) {
	d := state.Dir(t.TempDir())
	path := filepath.Join(string(d), "last_fetch")
	if err := os.WriteFile(path, []byte("not-a-time\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unparseable stamp reads as "never fetched": the worst
	// consequence is one extra fetch, always safe.
	if got, ok := d.LastFetch(); ok {
		t.Errorf("LastFetch on garbage = (%v, true), want (_, false)", got)
	}
}

func TestLeasesDir(t *testing.T) {
	d := state.Dir(filepath.Join("some", "root"))
	want := filepath.Join("some", "root", "leases")
	if got := d.LeasesDir(); got != want {
		t.Errorf("LeasesDir() = %q, want %q", got, want)
	}
}

func TestRefreshHashRoundTrip(t *testing.T) {
	d := state.Dir(t.TempDir())

	if got := d.RefreshHash("slot-1"); got != "" {
		t.Errorf("RefreshHash on fresh state = %q, want empty", got)
	}
	if err := d.WriteRefreshHash("slot-1", "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("slot-1"); got != "abc123" {
		t.Errorf("RefreshHash = %q, want %q", got, "abc123")
	}

	// The on-disk location is part of the documented state layout
	// (PLAN.md, State layout): trees/<name>/refresh_hash.
	onDisk := filepath.Join(string(d), "trees", "slot-1", "refresh_hash")
	if _, err := os.Stat(onDisk); err != nil {
		t.Errorf("hash not at the documented layout path: %v", err)
	}

	if err := d.WriteRefreshHash("slot-1", "def456"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("slot-1"); got != "def456" {
		t.Errorf("RefreshHash after overwrite = %q, want %q", got, "def456")
	}
}

func TestRefreshHashIsolatesTrees(t *testing.T) {
	d := state.Dir(t.TempDir())
	if err := d.WriteRefreshHash("slot-1", "aaa"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("slot-2"); got != "" {
		t.Errorf("RefreshHash(slot-2) = %q, want empty", got)
	}
}

func TestRemoveTree(t *testing.T) {
	d := state.Dir(t.TempDir())
	if err := d.WriteRefreshHash("slot-3", "aaa"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveTree("slot-3"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("slot-3"); got != "" {
		t.Errorf("RefreshHash after RemoveTree = %q, want empty", got)
	}
	// Removing state that never existed is not an error:
	// callers clean up unconditionally.
	if err := d.RemoveTree("never-existed"); err != nil {
		t.Errorf("RemoveTree on absent state: %v", err)
	}
}

func TestProvisionedMarker(t *testing.T) {
	d := state.Dir(t.TempDir())
	if d.Provisioned("slot-1") {
		t.Error("Provisioned on fresh state = true, want false")
	}
	if err := d.MarkProvisioned("slot-1"); err != nil {
		t.Fatal(err)
	}
	if !d.Provisioned("slot-1") {
		t.Error("Provisioned after MarkProvisioned = false, want true")
	}
	if d.Provisioned("slot-2") {
		t.Error("marker leaked across trees")
	}
	if err := d.RemoveTree("slot-1"); err != nil {
		t.Fatal(err)
	}
	if d.Provisioned("slot-1") {
		t.Error("Provisioned survived RemoveTree")
	}
}

func TestTreeNamesListsRecordedTrees(t *testing.T) {
	d := state.Dir(t.TempDir())

	names, err := d.TreeNames()
	if err != nil {
		t.Fatalf("TreeNames on fresh state: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("TreeNames on fresh state = %v, want none", names)
	}

	for _, name := range []string{"slot-2", "feature-x", "slot-1"} {
		if err := d.WriteRefreshHash(name, "abc"); err != nil {
			t.Fatal(err)
		}
	}
	names, err = d.TreeNames()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"feature-x", "slot-1", "slot-2"}
	if !slices.Equal(names, want) {
		t.Errorf("TreeNames() = %v, want %v (sorted)", names, want)
	}
}

func TestTreeNamesSkipsStrayFiles(t *testing.T) {
	d := state.Dir(t.TempDir())
	if err := d.WriteRefreshHash("real", "abc"); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(string(d), "trees", ".DS_Store")
	if err := os.WriteFile(stray, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := d.TreeNames()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"real"}; !slices.Equal(names, want) {
		t.Errorf("TreeNames() = %v, want %v (directories only)", names, want)
	}
}

func TestTreeDiskUsageRoundTrip(t *testing.T) {
	d := state.Dir(t.TempDir())

	if got, ok := d.TreeDiskUsage("feature-x"); ok {
		t.Errorf("TreeDiskUsage on fresh state = (%+v, true), want (_, false)", got)
	}

	want := state.DiskUsage{
		KB:         123456,
		MeasuredAt: time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC),
	}
	if err := d.WriteTreeDiskUsage("feature-x", want); err != nil {
		t.Fatal(err)
	}
	got, ok := d.TreeDiskUsage("feature-x")
	if !ok {
		t.Fatal("TreeDiskUsage after write = (_, false), want (_, true)")
	}
	if got.KB != want.KB || !got.MeasuredAt.Equal(want.MeasuredAt) {
		t.Errorf("TreeDiskUsage = %+v, want %+v", got, want)
	}

	// The on-disk location is part of the documented state layout
	// (PLAN.md, State layout): trees/<name>/du.
	onDisk := filepath.Join(string(d), "trees", "feature-x", "du")
	if _, err := os.Stat(onDisk); err != nil {
		t.Errorf("du cache not at the documented layout path: %v", err)
	}
}

func TestRootDiskUsageRoundTrip(t *testing.T) {
	d := state.Dir(t.TempDir())

	if got, ok := d.RootDiskUsage(); ok {
		t.Errorf("RootDiskUsage on fresh state = (%+v, true), want (_, false)", got)
	}

	want := state.DiskUsage{
		KB:         9,
		MeasuredAt: time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC),
	}
	if err := d.WriteRootDiskUsage(want); err != nil {
		t.Fatal(err)
	}
	got, ok := d.RootDiskUsage()
	if !ok {
		t.Fatal("RootDiskUsage after write = (_, false), want (_, true)")
	}
	if got.KB != want.KB || !got.MeasuredAt.Equal(want.MeasuredAt) {
		t.Errorf("RootDiskUsage = %+v, want %+v", got, want)
	}

	// A repo-wide fact lives at the state root, like last_fetch.
	onDisk := filepath.Join(string(d), "root_du")
	if _, err := os.Stat(onDisk); err != nil {
		t.Errorf("root du cache not at the documented layout path: %v", err)
	}
}

func TestDiskUsageIgnoresGarbage(t *testing.T) {
	d := state.Dir(t.TempDir())
	if err := d.WriteRefreshHash("feature-x", "abc"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(string(d), "trees", "feature-x", "du")
	if err := os.WriteFile(path, []byte("banana\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A corrupt cache reads as "never measured": the worst
	// consequence is one fresh du run, always safe.
	if got, ok := d.TreeDiskUsage("feature-x"); ok {
		t.Errorf("TreeDiskUsage on garbage = (%+v, true), want (_, false)", got)
	}

	// A negative size is corruption too, not a value to render.
	negative := []byte("-42 2026-07-25T09:30:00Z\n")
	if err := os.WriteFile(path, negative, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := d.TreeDiskUsage("feature-x"); ok {
		t.Errorf("TreeDiskUsage on negative size = (%+v, true), want (_, false)", got)
	}
}
