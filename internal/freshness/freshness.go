// Package freshness holds the pure staleness policy behind wt's
// base-branch fetching (PLAN.md D7): whether a recorded fetch has
// aged past the configured window, and how to say that age to a
// human. Kept pure and time-injected so both the display side
// (ls) and the decision side (new, claim, sync) share one rule and
// cannot drift.
package freshness

import (
	"fmt"
	"time"
)

// Stale reports whether a base fetched at last is due for another
// fetch, given the staleness window in hours and the current time.
// A zero last (never fetched) is always stale; a last in the future
// (clock skew) never is: wt just fetched, by its own record.
func Stale(last time.Time, hours int, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= time.Duration(hours)*time.Hour
}

// Age renders a duration coarsely, the way a fetch age is read: at
// a glance, not billed by the second. A non-positive duration (the
// event is now or in the future) reads as "just now".
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
