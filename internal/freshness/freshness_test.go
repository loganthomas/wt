package freshness_test

import (
	"testing"
	"time"

	"github.com/loganthomas/wt/internal/freshness"
)

func TestStale(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		last  time.Time
		hours int
		want  bool
	}{
		{"never fetched is stale", time.Time{}, 24, true},
		{"fetched an hour ago is fresh", now.Add(-1 * time.Hour), 24, false},
		{"fetched just under the window is fresh", now.Add(-23 * time.Hour), 24, false},
		{"fetched exactly at the window is stale", now.Add(-24 * time.Hour), 24, true},
		{"fetched past the window is stale", now.Add(-48 * time.Hour), 24, true},
		{"a tighter window trips sooner", now.Add(-2 * time.Hour), 1, true},
		// A last_fetch in the future (clock skew) is never stale:
		// wt just fetched, by its own record.
		{"a future timestamp is fresh", now.Add(1 * time.Hour), 24, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := freshness.Stale(tt.last, tt.hours, now); got != tt.want {
				t.Errorf("Stale(%v, %d) = %v, want %v", tt.last, tt.hours, got, tt.want)
			}
		})
	}
}

func TestAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
		// A negative duration (the event is in the future) reads as
		// just now rather than a nonsense "-1h ago".
		{-1 * time.Hour, "just now"},
	}
	for _, tt := range tests {
		if got := freshness.Age(tt.d); got != tt.want {
			t.Errorf("Age(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
