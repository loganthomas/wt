package config

import (
	"os"

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
func Save(path string, cfg Config) error {
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
	return os.WriteFile(path, raw, 0o644)
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
