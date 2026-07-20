package config

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// repoFile is the on-disk shape written by Save: pointers where a
// zero value should omit the whole table, omitempty elsewhere,
// so a fresh config stays as small as what the user chose.
type repoFile struct {
	Base     string   `toml:"base,omitempty"`
	TreesDir string   `toml:"trees_dir,omitempty"`
	Copy     []string `toml:"copy,omitempty"`
	Hooks    *Hooks   `toml:"hooks,omitempty"`
	Pool     *Pool    `toml:"pool,omitempty"`
}

// renderFile is the effective-config shape printed by Render:
// always-on keys so the reader sees every value in play,
// optional tables only when present.
type renderFile struct {
	Base     string   `toml:"base"`
	TreesDir string   `toml:"trees_dir"`
	Copy     []string `toml:"copy"`
	Hooks    *Hooks   `toml:"hooks,omitempty"`
	Pool     *Pool    `toml:"pool,omitempty"`
	UI       UI       `toml:"ui"`
}

// Save validates cfg and writes its repo-file keys to path.
// The UI section is global-only and never written here.
// Normalized like Load, so the file on disk always carries the
// same canonical spellings a later load would produce.
func Save(path string, cfg Config) error {
	// normalize rewrites list entries in place; cloning first
	// keeps the caller's slices untouched, as the by-value
	// signature promises.
	cfg.Copy = slices.Clone(cfg.Copy)
	cfg.Hooks.RefreshIfChanged = slices.Clone(cfg.Hooks.RefreshIfChanged)
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return err
	}
	file := repoFile{
		Base:     cfg.Base,
		TreesDir: cfg.TreesDir,
		Copy:     cfg.Copy,
		Hooks:    nilIfZero(cfg.Hooks),
		Pool:     cfg.Pool,
	}
	raw, err := toml.Marshal(file)
	if err != nil {
		return err
	}
	return writeAtomic(path, raw)
}

// writeAtomic lands the bytes via temp file and rename, so a
// crash mid-write can never leave a half-written wt.toml that
// every later command would choke on.
func writeAtomic(path string, raw []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(raw); err != nil {
		return err
	}
	// The rename must not clobber a mode the user chose; a fresh
	// file gets the plain 0o644 (CreateTemp opens 0o600).
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Render returns the merged config as TOML for `wt config`.
func Render(cfg Config) (string, error) {
	file := renderFile{
		Base:     cfg.Base,
		TreesDir: cfg.TreesDir,
		Copy:     orEmpty(cfg.Copy),
		Hooks:    nilIfZero(cfg.Hooks),
		Pool:     cfg.Pool,
		UI:       cfg.UI,
	}
	raw, err := toml.Marshal(file)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func nilIfZero(h Hooks) *Hooks {
	if h.Setup == "" && h.Refresh == "" && len(h.RefreshIfChanged) == 0 {
		return nil
	}
	return &h
}

// orEmpty pins a nil slice to an empty one so the rendered TOML
// still shows the key (`copy = []`) instead of dropping it.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
