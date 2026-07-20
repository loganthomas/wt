// Package state owns the layout of wt's per-repo state directory
// (PLAN.md D4): lease directories, per-tree refresh hashes, and,
// in later phases, fetch timestamps. Every path under the state
// root is spelled here and nowhere else, so the on-disk layout
// documented in PLAN.md cannot drift piecemeal.
package state

import (
	"os"
	"path/filepath"
	"strings"
)

// Dir is one repository's state root,
// e.g. ~/.local/state/wt/repos/<slug>-<hash8>.
type Dir string

// LeasesDir is where pool slot leases live; the lease package
// manages its contents.
func (d Dir) LeasesDir() string {
	return filepath.Join(string(d), "leases")
}

// RefreshHash returns the hash recorded for tree name at its last
// successful refresh, or "" when none has been recorded.
// Any read failure reads as "no hash": the worst consequence is
// one redundant refresh run, which is always safe.
func (d Dir) RefreshHash(name string) string {
	raw, err := os.ReadFile(d.refreshHashPath(name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// WriteRefreshHash records the refresh hash for tree name,
// creating the tree's state directory as needed.
func (d Dir) WriteRefreshHash(name, hash string) error {
	path := d.refreshHashPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hash+"\n"), 0o644)
}

// RemoveTree drops all recorded state for tree name;
// absent state is not an error, so cleanup can run unconditionally.
func (d Dir) RemoveTree(name string) error {
	return os.RemoveAll(d.treeDir(name))
}

func (d Dir) treeDir(name string) string {
	return filepath.Join(string(d), "trees", name)
}

func (d Dir) refreshHashPath(name string) string {
	return filepath.Join(d.treeDir(name), "refresh_hash")
}
