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
// Notes are grouped by the proposal they describe, so the caller
// prints only the ones whose proposal actually landed; info notes
// propose nothing and always print.
type detected struct {
	refresh   string
	gate      []string
	hookNote  string
	copies    []string
	copyNotes []string
	infoNotes []string
}

// detectDefaults scans root for well-known markers and proposes
// init defaults. tracked reports which candidate files the index
// owns; nil means unknown, which proposes nothing that depends on
// it. Hooks require a tracked marker: an untracked lockfile never
// reaches a fresh tree, so its gate would hash as absent forever
// and the hook would run once and then never again. Copies
// require the opposite (a tracked .env travels with checkouts and
// needs no copying).
func detectDefaults(root string, tracked map[string]bool) detected {
	var d detected
	for _, e := range ecosystems {
		if tracked == nil || !tracked[e.marker] || !present(root, e.marker) {
			continue
		}
		d.refresh, d.gate = e.hook, []string{e.marker}
		d.hookNote = fmt.Sprintf(
			"detected %s — proposing refresh hook %q gated on it", e.marker, e.hook)
		break
	}
	for _, m := range sharedCacheMarkers {
		if present(root, m.marker) {
			d.infoNotes = append(d.infoNotes, m.note)
		}
	}
	if tracked == nil {
		return d
	}
	for _, name := range copyCandidates {
		if !present(root, name) || tracked[name] {
			continue
		}
		d.copies = append(d.copies, name)
		d.copyNotes = append(d.copyNotes, fmt.Sprintf(
			"detected untracked %s — proposing it for the copy list", name))
	}
	return d
}

// present reports whether name exists at the repo root.
func present(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

// detectTracked reports which detection candidates (ecosystem
// markers and copy files) the index owns; nil on error, which
// detectDefaults reads as "unknown, propose none".
func detectTracked(ctx context.Context, r *repo.Repo) map[string]bool {
	names := make([]string, 0, len(copyCandidates)+len(ecosystems))
	names = append(names, copyCandidates...)
	for _, e := range ecosystems {
		names = append(names, e.marker)
	}
	tracked, err := gitx.New(r.Root).Tracked(ctx, names...)
	if err != nil {
		return nil
	}
	return tracked
}
