// Base-branch freshness (PLAN.md D7): the opportunistic fetch that
// wt new and wt claim run when the base has gone stale, and the
// staleness note wt ls displays without ever touching the network.
// The fast-forward itself is deliberately not here — that is the
// explicit wt sync's job — so these paths never mutate a working
// tree behind the user's back.
package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/freshness"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/repo"
	"github.com/loganthomas/wt/internal/state"
)

// maybeFetchBase fetches base's remote when the last fetch has aged
// past the staleness window, unless noFetch is set. It is
// deliberately best-effort: a base that tracks no remote is nothing
// to fetch, and a fetch that fails (offline) warns and lets the
// command proceed on the local base rather than blocking work.
// After a fetch it notes when the local base now trails upstream,
// pointing at wt sync — it never fast-forwards here.
func maybeFetchBase(
	ctx context.Context, g *gitx.Git, cfg config.Config, st state.Dir,
	base string, noFetch bool, chatter io.Writer,
) {
	if noFetch {
		return
	}
	remote, err := g.UpstreamRemote(ctx, base)
	if err != nil {
		return
	}
	last, _ := st.LastFetch()
	if !freshness.Stale(last, cfg.StalenessWindow(), time.Now()) {
		return
	}
	fmt.Fprintf(chatter, "base %s %s — fetching %s\n", base, lastFetchPhrase(last), remote)
	if err := g.Fetch(ctx, remote); err != nil {
		fmt.Fprintf(chatter, "fetch failed: %v — continuing on the local base\n", err)
		return
	}
	// The stamp is an optimization, not load-bearing: recording it is
	// best-effort, and the behind notice runs regardless so a failed
	// write never also swallows the "you're behind" pointer.
	warnFetchRecord(st.WriteLastFetch(time.Now()), chatter)
	noteBehindUpstream(ctx, g, base, chatter)
}

// warnFetchRecord surfaces a failed last_fetch write as a note and
// otherwise stays quiet. A missed stamp only costs one extra fetch
// next time (state.LastFetch fails open), so it must never abort the
// work the fetch was for.
func warnFetchRecord(err error, chatter io.Writer) {
	if err != nil {
		fmt.Fprintf(chatter,
			"note: could not record the fetch time (%v); wt will refetch next time\n", err)
	}
}

// noteBehindUpstream points at wt sync when the local base now
// trails its upstream, the one action maybeFetchBase leaves to the
// user (it never mutates a working tree itself).
func noteBehindUpstream(ctx context.Context, g *gitx.Git, base string, chatter io.Writer) {
	up, err := g.Upstream(ctx, base)
	if err != nil {
		return
	}
	n, err := g.CommitCount(ctx, base+".."+up)
	if err != nil || n == 0 {
		return
	}
	fmt.Fprintf(chatter, "%s is %s behind %s — `wt sync` to fast-forward\n",
		base, commits(n), up)
}

// noteFetchStaleness prints one line about the base's fetch age for
// wt ls, and only when wt has a fetch on record: it never touches
// the network (D7), so a base wt has never fetched has nothing to
// report. Best-effort throughout — a broken config or unreadable
// state leaves the listing itself untouched.
func noteFetchStaleness(r *repo.Repo, chatter io.Writer) {
	cfg, err := loadMerged(r)
	if err != nil {
		return
	}
	sd, err := r.StateDir()
	if err != nil {
		return
	}
	last, ok := state.Dir(sd).LastFetch()
	if !ok {
		return
	}
	now := time.Now()
	msg := fmt.Sprintf("base %s fetched %s", cfg.Base, freshness.Age(now.Sub(last)))
	if freshness.Stale(last, cfg.StalenessWindow(), now) {
		msg += " — stale, `wt sync` to refresh"
	}
	fmt.Fprintln(chatter, msg)
}

// lastFetchPhrase describes a base's fetch age for the fetch notice,
// spelling out the never-fetched case rather than saying "just now".
func lastFetchPhrase(last time.Time) string {
	if last.IsZero() {
		return "not yet fetched"
	}
	return "last fetched " + freshness.Age(time.Since(last))
}

// commits pluralizes a commit count for user-facing notices.
func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}
