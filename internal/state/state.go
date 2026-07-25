// Package state owns the layout of wt's per-repo state directory
// (PLAN.md D4): lease directories, per-tree refresh hashes, and,
// in later phases, fetch timestamps. Every path under the state
// root is spelled here and nowhere else, so the on-disk layout
// documented in PLAN.md cannot drift piecemeal.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Dir is one repository's state root,
// e.g. ~/.local/state/wt/repos/<slug>-<hash8>.
type Dir string

// LeasesDir is where pool slot leases live; the lease package
// manages its contents.
func (d Dir) LeasesDir() string {
	return filepath.Join(string(d), "leases")
}

// lastFetchFile records when wt last fetched the base (PLAN.md D7),
// a repo-wide fact and so a single file at the state root rather
// than a per-tree one.
const lastFetchFile = "last_fetch"

// LastFetch returns when wt last fetched the base, and whether any
// fetch is on record. A missing or unparseable stamp reads as
// "never fetched": the worst consequence is one extra fetch, always
// safe, and the same fail-open rule the refresh hash follows.
func (d Dir) LastFetch() (time.Time, bool) {
	raw, err := os.ReadFile(filepath.Join(string(d), lastFetchFile))
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// WriteLastFetch records t as the base's last fetch time, creating
// the state root as needed. Stored to RFC3339 seconds, all a
// staleness window in hours ever needs.
func (d Dir) WriteLastFetch(t time.Time) error {
	if err := os.MkdirAll(string(d), 0o755); err != nil {
		return err
	}
	stamp := t.UTC().Format(time.RFC3339) + "\n"
	return os.WriteFile(filepath.Join(string(d), lastFetchFile), []byte(stamp), 0o644)
}

// The per-tree files. Named once: this package's whole job is
// keeping the on-disk layout in one place.
const (
	refreshHashFile = "refresh_hash"
	provisionedFile = "provisioned"
)

// RefreshHash returns the hash recorded for tree name at its last
// successful refresh, or "" when none has been recorded.
// Any read failure reads as "no hash": the worst consequence is
// one redundant refresh run, which is always safe.
func (d Dir) RefreshHash(name string) string {
	raw, err := os.ReadFile(d.treeFile(name, refreshHashFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// WriteRefreshHash records the refresh hash for tree name,
// creating the tree's state directory as needed.
func (d Dir) WriteRefreshHash(name, hash string) error {
	return d.writeTreeFile(name, refreshHashFile, []byte(hash+"\n"))
}

// MarkProvisioned records that tree name finished provisioning:
// worktree, copies, setup hook, all of it. Written last, so its
// absence on a registered slot proves the provision died midway.
func (d Dir) MarkProvisioned(name string) error {
	return d.writeTreeFile(name, provisionedFile, nil)
}

// Provisioned reports whether tree name completed provisioning.
func (d Dir) Provisioned(name string) bool {
	_, err := os.Stat(d.treeFile(name, provisionedFile))
	return err == nil
}

// TreeNames lists every tree with recorded state, sorted, so
// cleanup can reconcile the records against git's live worktree
// list (R8). Only directories count: a stray file under trees/
// is not a tree's state.
func (d Dir) TreeNames() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(string(d), "trees"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// DiskUsage is one measured tree size, cached so wt status does
// not rerun du over a 750k-file tree on every invocation.
type DiskUsage struct {
	KB         int64
	MeasuredAt time.Time
}

// The du cache files: per tree under its state directory, and at
// the state root for the main checkout, which is repo-wide like
// last_fetch.
const (
	duFile     = "du"
	rootDuFile = "root_du"
)

// TreeDiskUsage returns the cached size of tree name, and whether
// one is on record. A missing or corrupt cache reads as "never
// measured": the worst consequence is one fresh du run, always safe.
func (d Dir) TreeDiskUsage(name string) (DiskUsage, bool) {
	return readDiskUsage(d.treeFile(name, duFile))
}

// WriteTreeDiskUsage caches tree name's measured size.
func (d Dir) WriteTreeDiskUsage(name string, u DiskUsage) error {
	return d.writeTreeFile(name, duFile, formatDiskUsage(u))
}

// RootDiskUsage returns the main checkout's cached size, and
// whether one is on record.
func (d Dir) RootDiskUsage() (DiskUsage, bool) {
	return readDiskUsage(filepath.Join(string(d), rootDuFile))
}

// WriteRootDiskUsage caches the main checkout's measured size,
// creating the state root as needed.
func (d Dir) WriteRootDiskUsage(u DiskUsage) error {
	if err := os.MkdirAll(string(d), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(string(d), rootDuFile), formatDiskUsage(u), 0o644)
}

func readDiskUsage(path string) (DiskUsage, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DiskUsage{}, false
	}
	var kb int64
	var stamp string
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d %s", &kb, &stamp); err != nil {
		return DiskUsage{}, false
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return DiskUsage{}, false
	}
	return DiskUsage{KB: kb, MeasuredAt: at}, true
}

func formatDiskUsage(u DiskUsage) []byte {
	return fmt.Appendf(nil, "%d %s\n", u.KB, u.MeasuredAt.UTC().Format(time.RFC3339))
}

// RemoveTree drops all recorded state for tree name;
// absent state is not an error, so cleanup can run unconditionally.
func (d Dir) RemoveTree(name string) error {
	return os.RemoveAll(d.treeDir(name))
}

func (d Dir) writeTreeFile(name, file string, data []byte) error {
	path := d.treeFile(name, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (d Dir) treeFile(name, file string) string {
	return filepath.Join(d.treeDir(name), file)
}

func (d Dir) treeDir(name string) string {
	return filepath.Join(string(d), "trees", name)
}
