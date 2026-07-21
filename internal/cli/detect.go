// Repo-root detection behind wt init's proposed defaults: users
// rarely know what "warm" means for their tree in wt's terms, but
// their lockfiles do. Everything found here is only a proposal;
// the form and the flags override every value, and an empty scan
// proposes nothing.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/repo"
)

// ecosystems maps a root marker file to the refresh hook it
// implies, most specific first: the first marker present wins, so
// a repo carrying several lockfiles gets the most telling one.
// Only refresh is proposed, never setup: on a fresh tree the
// refresh hook runs anyway (finishFresh), and real setup hooks
// (make bootstrap) are exactly what a scan cannot guess.
var ecosystems = []struct {
	marker string
	hook   string
}{
	{"pnpm-lock.yaml", "pnpm install"},
	{"package-lock.json", "npm ci"},
	{"yarn.lock", "yarn install"},
	{"uv.lock", "uv sync"},
	{"poetry.lock", "poetry install"},
	{"Cargo.lock", "cargo fetch"},
}

// sharedCacheMarkers name ecosystems whose expensive state is
// already machine-wide: nothing for wt to configure, but saying
// so heads off the "will my trees share caches?" question.
var sharedCacheMarkers = []struct{ marker, note string }{
	{"MODULE.bazel", "detected Bazel — a shared disk cache is the win here; see docs/recipes.md"},
	{"go.mod", "detected Go — module and build caches are already machine-wide; no hooks needed"},
}

// copyCandidates are the well-known untracked files a new tree
// needs a copy of.
var copyCandidates = []string{".env", ".envrc", ".env.local"}

// detected is what the repo scan proposes for the init form.
type detected struct {
	refresh string
	gate    []string
	copies  []string
	notes   []string
}

// detectDefaults scans root for well-known markers and proposes
// init defaults. tracked reports which copyCandidates the index
// owns (a tracked .env travels with checkouts and needs no
// copying); nil means unknown, which proposes none.
func detectDefaults(root string, tracked map[string]bool) detected {
	var d detected
	for _, e := range ecosystems {
		if _, err := os.Stat(filepath.Join(root, e.marker)); err != nil {
			continue
		}
		d.refresh, d.gate = e.hook, []string{e.marker}
		d.notes = append(d.notes, fmt.Sprintf(
			"detected %s — proposing refresh hook %q gated on it", e.marker, e.hook))
		break
	}
	for _, m := range sharedCacheMarkers {
		if _, err := os.Stat(filepath.Join(root, m.marker)); err == nil {
			d.notes = append(d.notes, m.note)
		}
	}
	if tracked == nil {
		return d
	}
	for _, name := range copyCandidates {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil || tracked[name] {
			continue
		}
		d.copies = append(d.copies, name)
		d.notes = append(d.notes, fmt.Sprintf(
			"detected untracked %s — proposing it for the copy list", name))
	}
	return d
}

// trackedCopyCandidates reports which of the well-known copy
// files the index owns; nil on error, which detectDefaults reads
// as "unknown, propose none".
func trackedCopyCandidates(ctx context.Context, r *repo.Repo) map[string]bool {
	tracked, err := gitx.New(r.Root).Tracked(ctx, copyCandidates...)
	if err != nil {
		return nil
	}
	return tracked
}
