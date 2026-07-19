package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		global  string // TOML content; empty means the file is absent
		repo    string
		want    Config
		wantErr string // substring of the expected error; empty means success
	}{
		{
			name: "defaults when no files exist",
			want: Config{Base: "main", UI: UI{Color: "auto"}},
		},
		{
			name: "global defaults apply",
			global: `[defaults]
base = "trunk"
copy = [".env"]
`,
			want: Config{Base: "trunk", Copy: []string{".env"}, UI: UI{Color: "auto"}},
		},
		{
			name: "repo overrides global",
			global: `[defaults]
base = "trunk"
trees_dir = "../global.trees"
`,
			repo: `base = "green"
trees_dir = "../acme.trees"
`,
			want: Config{Base: "green", TreesDir: "../acme.trees", UI: UI{Color: "auto"}},
		},
		{
			name: "repo copy replaces global copy",
			global: `[defaults]
copy = [".env"]
`,
			repo: `copy = [".envrc", ".env.local"]
`,
			want: Config{Copy: []string{".envrc", ".env.local"}, Base: "main", UI: UI{Color: "auto"}},
		},
		{
			name: "hooks and pool table enable pool mode",
			repo: `[hooks]
setup              = "make bootstrap"
refresh            = "pnpm install"
refresh_if_changed = ["pnpm-lock.yaml"]

[pool]
size = 6
`,
			want: Config{
				Base: "main",
				Hooks: Hooks{
					Setup:            "make bootstrap",
					Refresh:          "pnpm install",
					RefreshIfChanged: []string{"pnpm-lock.yaml"},
				},
				Pool: &Pool{Size: 6},
				UI:   UI{Color: "auto"},
			},
		},
		{
			name:    "pool size below one rejected",
			repo:    "[pool]\nsize = 0\n",
			wantErr: "pool.size",
		},
		{
			name:    "pool table without size rejected",
			repo:    "[pool]\n",
			wantErr: "pool.size",
		},
		{
			name: "pool under global defaults rejected",
			global: `[defaults.pool]
size = 4
`,
			wantErr: "per-repo",
		},
		{
			name:    "unknown repo key reports file and position",
			repo:    "base = \"main\"\nbogus = true\n",
			wantErr: "wt.toml:2:",
		},
		{
			name:    "syntax error reports file and position",
			repo:    "base = \"main\nbroken\n",
			wantErr: "wt.toml:1:",
		},
		{
			name:    "type error reports file and position",
			repo:    "copy = \"not-a-list\"\n",
			wantErr: "wt.toml:1:",
		},
		{
			name:    "absolute copy path rejected",
			repo:    "copy = [\"/etc/passwd\"]\n",
			wantErr: "copy",
		},
		{
			name:    "copy path escaping the tree rejected",
			repo:    "copy = [\"../secrets\"]\n",
			wantErr: "copy",
		},
		{
			name: "ui color from global",
			global: `[ui]
color = "never"
`,
			want: Config{Base: "main", UI: UI{Color: "never"}},
		},
		{
			name:    "invalid ui color rejected",
			global:  "[ui]\ncolor = \"sometimes\"\n",
			wantErr: "ui.color",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			globalPath := writeOptional(t, dir, "config.toml", tt.global)
			repoPath := writeOptional(t, dir, "wt.toml", tt.repo)

			got, err := Load(globalPath, repoPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() = %+v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Load() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// writeOptional writes content under dir and returns its path.
// Empty content returns a path to a file that does not exist,
// exercising the missing-file branches of Load.
func writeOptional(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if content == "" {
		return path
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGlobalPath(t *testing.T) {
	t.Run("respects XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
		got, err := GlobalPath()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/xdg/config", "wt", "config.toml"); got != want {
			t.Errorf("GlobalPath() = %q, want %q", got, want)
		}
	})
	t.Run("falls back to ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		got, err := GlobalPath()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, ".config", "wt", "config.toml"); got != want {
			t.Errorf("GlobalPath() = %q, want %q", got, want)
		}
	})
}
