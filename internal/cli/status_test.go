package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loganthomas/wt/internal/lease"
)

var errTest = errors.New("boom")

func TestHumanKB(t *testing.T) {
	tests := []struct {
		kb   int64
		want string
	}{
		{0, "0K"},
		{999, "999K"},
		{1024, "1.0M"},
		{55296, "54.0M"},
		{1048576, "1.0G"},
		{1782579, "1.7G"},
	}
	for _, tt := range tests {
		if got := humanKB(tt.kb); got != tt.want {
			t.Errorf("humanKB(%d) = %q, want %q", tt.kb, got, tt.want)
		}
	}
}

func TestFormatStatusDefaultMode(t *testing.T) {
	kb := int64(2048)
	view := statusView{
		Mode: "default",
		Base: baseView{Name: "main", Stale: true},
		Trees: []treeStatus{
			{Branch: "main", Path: "/repo", DiskKB: &kb},
			{Branch: "feature/login", Path: "/repo.trees/feature-login"},
		},
	}
	got := formatStatus(view)
	for _, want := range []string{
		"mode  default",
		"base  main — not yet fetched",
		"main", "/repo", "2.0M",
		"feature/login",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatStatus() missing %q:\n%s", want, got)
		}
	}
	// A size never measured renders as ?, not as a fake zero.
	if !strings.Contains(got, "?") {
		t.Errorf("formatStatus() missing ? for the unmeasured tree:\n%s", got)
	}
}

func TestFormatStatusPoolModeListsSlots(t *testing.T) {
	fetched := time.Now().Add(-2 * time.Hour)
	view := statusView{
		Mode: "pool",
		Base: baseView{Name: "main", LastFetch: &fetched},
		Pool: &poolStatus{
			Size: 2,
			Slots: []slotView{
				{Slot: "slot-1", State: "claimed", Branch: "feat-x", PID: 42},
				{Slot: "slot-2", State: "free"},
			},
		},
	}
	got := formatStatus(view)
	for _, want := range []string{
		"mode  pool (2 slots)",
		"last fetched 2h ago",
		"slot-1", "claimed", "feat-x",
		"slot-2", "free",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatStatus() missing %q:\n%s", want, got)
		}
	}
}

// The slot views feed wt pool ls and wt status both, so the
// classification must cover every occupancy state.
func TestSlotViewClassifiesOccupancy(t *testing.T) {
	live := &lease.Info{PID: 1, Branch: "feat-x", ClaimedAt: time.Now()}
	tests := []struct {
		name       string
		registered bool
		held       *lease.Info
		err        error
		wantState  string
		wantBranch string
	}{
		{"unreadable record", true, nil, errTest, "claimed", "?"},
		{"no lease, no tree", false, nil, nil, "unprovisioned", ""},
		{"no lease, tree parked", true, nil, nil, "free", ""},
		{"live claim", true, live, nil, "claimed", "feat-x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newSlotView("slot-1", tt.registered, tt.held, tt.err)
			if v.State != tt.wantState || v.Branch != tt.wantBranch {
				t.Errorf("newSlotView() = state %q branch %q, want %q %q",
					v.State, v.Branch, tt.wantState, tt.wantBranch)
			}
		})
	}
}
