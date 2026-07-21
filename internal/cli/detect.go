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

	"github.com/spf13/pflag"

	"github.com/loganthomas/wt/internal/config"
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

// copyCandidates are the well-known untracked files a new tree
// needs a copy of.
var copyCandidates = []string{".env", ".envrc", ".env.local"}

// detected is what the repo scan proposes for the init form.
// The winning marker doubles as the refresh gate, so the two
// cannot drift from the hook they belong to.
type detected struct {
	marker  string // ecosystem marker that won, "" when none
	refresh string
	copies  []string
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
		d.marker, d.refresh = e.marker, e.hook
		break
	}
	if tracked == nil {
		return d
	}
	for _, name := range copyCandidates {
		if !present(root, name) || tracked[name] {
			continue
		}
		d.copies = append(d.copies, name)
	}
	return d
}

// applyDetected settles the hook and copy values across the three
// layers, most explicit first: flags, then global defaults, then
// what the scan proposes, so users confirm recognizable values
// instead of inventing them. A changed flag wins even when its
// value is empty, so an empty --refresh declines the scan's
// proposal and keeps it out of the repo file. It cannot unset a
// global default: by the layering contract an empty value never
// overrides, so a global hook merges back in at load time and the
// global config is where to remove it. It returns the notes worth
// printing: only the proposals that survived, because advertising
// a value that flags or global config then beat would misstate
// what was configured.
func applyDetected(
	opts *initOptions, flags *pflag.FlagSet, seed config.Config, det detected,
) []string {
	if !flags.Changed("setup") {
		opts.setup = seed.Hooks.Setup
	}
	detHook := false
	if !flags.Changed("refresh") {
		opts.refresh = seed.Hooks.Refresh
		if opts.refresh == "" {
			opts.refresh = det.refresh
			detHook = opts.refresh != ""
		}
	}
	// The detected gate travels only with the detected hook:
	// pinning a hook from another layer to a lockfile it knows
	// nothing about would silently skip it on unchanged claims.
	if !flags.Changed("refresh-if-changed") {
		opts.refreshGate = seed.Hooks.RefreshIfChanged
		if opts.refreshGate == nil && detHook {
			opts.refreshGate = []string{det.marker}
		}
	}
	detCopies := false
	if !flags.Changed("copy") {
		opts.copyList = seed.Copy
		if opts.copyList == nil {
			opts.copyList = det.copies
			detCopies = opts.copyList != nil
		}
	}

	var notes []string
	if detHook {
		notes = append(notes, fmt.Sprintf(
			"detected %s — proposing refresh hook %q gated on it", det.marker, det.refresh))
	}
	if detCopies {
		for _, name := range det.copies {
			notes = append(notes, fmt.Sprintf(
				"detected untracked %s — proposing it for the copy list", name))
		}
	}
	return notes
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
