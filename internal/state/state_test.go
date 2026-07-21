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
