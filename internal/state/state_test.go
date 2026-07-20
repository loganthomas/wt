package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/loganthomas/wt/internal/state"
)

func TestLeasesDir(t *testing.T) {
	d := state.Dir(filepath.Join("some", "root"))
	want := filepath.Join("some", "root", "leases")
	if got := d.LeasesDir(); got != want {
		t.Errorf("LeasesDir() = %q, want %q", got, want)
	}
}

func TestRefreshHashRoundTrip(t *testing.T) {
	d := state.Dir(t.TempDir())

	if got := d.RefreshHash("pool-1"); got != "" {
		t.Errorf("RefreshHash on fresh state = %q, want empty", got)
	}
	if err := d.WriteRefreshHash("pool-1", "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("pool-1"); got != "abc123" {
		t.Errorf("RefreshHash = %q, want %q", got, "abc123")
	}

	// The on-disk location is part of the documented state layout
	// (PLAN.md, State layout): trees/<name>/refresh_hash.
	onDisk := filepath.Join(string(d), "trees", "pool-1", "refresh_hash")
	if _, err := os.Stat(onDisk); err != nil {
		t.Errorf("hash not at the documented layout path: %v", err)
	}

	if err := d.WriteRefreshHash("pool-1", "def456"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("pool-1"); got != "def456" {
		t.Errorf("RefreshHash after overwrite = %q, want %q", got, "def456")
	}
}

func TestRefreshHashIsolatesTrees(t *testing.T) {
	d := state.Dir(t.TempDir())
	if err := d.WriteRefreshHash("pool-1", "aaa"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("pool-2"); got != "" {
		t.Errorf("RefreshHash(pool-2) = %q, want empty", got)
	}
}

func TestRemoveTree(t *testing.T) {
	d := state.Dir(t.TempDir())
	if err := d.WriteRefreshHash("pool-3", "aaa"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveTree("pool-3"); err != nil {
		t.Fatal(err)
	}
	if got := d.RefreshHash("pool-3"); got != "" {
		t.Errorf("RefreshHash after RemoveTree = %q, want empty", got)
	}
	// Removing state that never existed is not an error:
	// callers clean up unconditionally.
	if err := d.RemoveTree("never-existed"); err != nil {
		t.Errorf("RemoveTree on absent state: %v", err)
	}
}
