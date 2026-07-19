// Package config loads and merges wt's TOML configuration.
//
// Two files feed one merged Config (see PLAN.md D4):
// global defaults at ~/.config/wt/config.toml under [defaults],
// and per-repo settings at <git-common-dir>/wt.toml.
// Merge order is built-in → global → repo;
// scalars replace when set, lists replace whole.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Config is the merged, validated configuration for one repository.
type Config struct {
	Base     string   `toml:"base"`
	TreesDir string   `toml:"trees_dir"`
	Copy     []string `toml:"copy"`
	Hooks    Hooks    `toml:"hooks"`
	// Pool being non-nil is what puts a repo in pool mode (D3);
	// there is no separate mode setting.
	Pool *Pool `toml:"pool"`
	// UI comes only from the global file's [ui] table,
	// so it is invisible to the per-repo decoder.
	UI UI `toml:"-"`
}

// Hooks are the user commands run around tree creation and refresh.
type Hooks struct {
	Setup            string   `toml:"setup,omitempty"`
	Refresh          string   `toml:"refresh,omitempty"`
	RefreshIfChanged []string `toml:"refresh_if_changed,omitempty"`
}

// Pool configures the pre-warmed slot pool for monorepos.
type Pool struct {
	Size int `toml:"size"`
}

// UI holds presentation settings from the global config.
type UI struct {
	Color string `toml:"color"`
}

// Default is the built-in configuration every merge starts from.
func Default() Config {
	return Config{Base: "main", UI: UI{Color: "auto"}}
}

// globalFile is the shape of ~/.config/wt/config.toml.
type globalFile struct {
	Defaults Config `toml:"defaults"`
	UI       UI     `toml:"ui"`
}

// GlobalPath returns the global config file location,
// honoring $XDG_CONFIG_HOME with the ~/.config fallback.
// os.UserConfigDir is unsuitable here: on macOS it points at
// ~/Library/Application Support, and wt follows the XDG norm
// of its peers (zoxide, starship) instead.
func GlobalPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "wt", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "wt", "config.toml"), nil
}

// Load reads the global and repo config files, either of which
// may be absent, and returns the merged, validated result.
func Load(globalPath, repoPath string) (Config, error) {
	cfg := Default()

	var global globalFile
	if err := decodeFile(globalPath, &global); err != nil {
		return Config{}, err
	}
	if global.Defaults.Pool != nil {
		return Config{}, fmt.Errorf(
			"%s: pool mode is per-repo (D3); move [pool] into the repo's wt.toml", globalPath)
	}
	merge(&cfg, global.Defaults)
	if global.UI.Color != "" {
		cfg.UI.Color = global.UI.Color
	}

	var repo Config
	if err := decodeFile(repoPath, &repo); err != nil {
		return Config{}, err
	}
	merge(&cfg, repo)

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// merge overlays set values from layer onto cfg.
// Zero values mean "unset" and leave the lower layer in place;
// lists replace as a whole rather than appending,
// so a repo can narrow a global list, not only grow it.
func merge(cfg *Config, layer Config) {
	if layer.Base != "" {
		cfg.Base = layer.Base
	}
	if layer.TreesDir != "" {
		cfg.TreesDir = layer.TreesDir
	}
	if layer.Copy != nil {
		cfg.Copy = layer.Copy
	}
	if layer.Hooks.Setup != "" {
		cfg.Hooks.Setup = layer.Hooks.Setup
	}
	if layer.Hooks.Refresh != "" {
		cfg.Hooks.Refresh = layer.Hooks.Refresh
	}
	if layer.Hooks.RefreshIfChanged != nil {
		cfg.Hooks.RefreshIfChanged = layer.Hooks.RefreshIfChanged
	}
	if layer.Pool != nil {
		cfg.Pool = layer.Pool
	}
}

var uiColors = []string{"auto", "always", "never"}

func validate(cfg Config) error {
	if cfg.Pool != nil && cfg.Pool.Size < 1 {
		return fmt.Errorf("pool.size must be at least 1, got %d", cfg.Pool.Size)
	}
	if err := validateTreeLocal("copy", cfg.Copy); err != nil {
		return err
	}
	if err := validateTreeLocal("hooks.refresh_if_changed", cfg.Hooks.RefreshIfChanged); err != nil {
		return err
	}
	// Empty means unset: Save sees pre-merge configs whose UI
	// section (global-only) is legitimately absent.
	if cfg.UI.Color != "" && !slices.Contains(uiColors, cfg.UI.Color) {
		return fmt.Errorf("ui.color must be one of auto, always, never; got %q", cfg.UI.Color)
	}
	return nil
}

// validateTreeLocal rejects paths that could reach outside a worktree.
// Copy sources and hash inputs are meant to name files of the repo;
// an absolute or ..-escaping entry would read (or overwrite) files
// wt has no business touching.
func validateTreeLocal(key string, paths []string) error {
	for _, p := range paths {
		if !filepath.IsLocal(p) {
			return fmt.Errorf("%s entry %q must stay inside the tree (relative, no ..)", key, p)
		}
	}
	return nil
}
