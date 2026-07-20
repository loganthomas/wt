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
			wantErr: "per repository",
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
			name: "copy entries normalize to git's spelling",
			repo: "copy = [\"./.env\", \"dir//local.txt\"]\n",
			want: Config{
				Base: "main",
				Copy: []string{".env", "dir/local.txt"},
				UI:   UI{Color: "auto"},
			},
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

func TestSave(t *testing.T) {
	t.Run("writes only what is set and round-trips", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "wt.toml")
		in := Config{
			Base:     "green",
			TreesDir: "../acme.trees",
			Copy:     []string{".env"},
			Pool:     &Pool{Size: 6},
		}
		if err := Save(path, in); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "[hooks]") {
			t.Errorf("Save() wrote an empty [hooks] table:\n%s", raw)
		}
		got, err := Load(filepath.Join(t.TempDir(), "absent"), path)
		if err != nil {
			t.Fatal(err)
		}
		in.UI = UI{Color: "auto"}
		if diff := cmp.Diff(in, got); diff != "" {
			t.Errorf("round-trip mismatch (-saved +loaded):\n%s", diff)
		}
	})
	t.Run("leaves no temp file behind", func(t *testing.T) {
		dir := t.TempDir()
		if err := Save(filepath.Join(dir, "wt.toml"), Config{Base: "main"}); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "wt.toml" {
			t.Errorf("Save() left extra files in %s: %v", dir, entries)
		}
	})
	t.Run("refuses invalid config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "wt.toml")
		err := Save(path, Config{Pool: &Pool{Size: -1}})
		if err == nil || !strings.Contains(err.Error(), "pool.size") {
			t.Errorf("Save(bad pool) error = %v, want pool.size complaint", err)
		}
		if _, statErr := os.Stat(path); statErr == nil {
			t.Error("Save(bad pool) still wrote the file")
		}
	})
}

func TestRender(t *testing.T) {
	got, err := Render(Config{
		Base:     "main",
		TreesDir: "/abs/acme.trees",
		Hooks:    Hooks{Setup: "make bootstrap"},
		UI:       UI{Color: "auto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `base = 'main'
trees_dir = '/abs/acme.trees'
copy = []

[hooks]
setup = 'make bootstrap'

[ui]
color = 'auto'
`
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Render() mismatch (-want +got):\n%s", diff)
	}
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
