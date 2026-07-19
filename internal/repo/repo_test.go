package repo

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/loganthomas/wt/internal/gittest"
)

func TestFind(t *testing.T) {
	gittest.Scrub(t)
	work := gittest.TempDir(t)
	root := gittest.Repo(t, filepath.Join(work, "acme"))

	t.Run("from the repo root", func(t *testing.T) {
		r, err := Find(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, ".git"); r.CommonDir != want {
			t.Errorf("CommonDir = %q, want %q", r.CommonDir, want)
		}
		if r.Root != root {
			t.Errorf("Root = %q, want %q", r.Root, root)
		}
	})

	t.Run("from a subdirectory", func(t *testing.T) {
		sub := filepath.Join(root, "cmd", "deep")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		r, err := Find(t.Context(), sub)
		if err != nil {
			t.Fatal(err)
		}
		if r.Root != root {
			t.Errorf("Root = %q, want %q", r.Root, root)
		}
	})

	t.Run("from a linked worktree", func(t *testing.T) {
		linked := filepath.Join(work, "acme.trees", "feature")
		gittest.Run(t, root, "worktree", "add", "-q", linked, "-b", "feature")
		r, err := Find(t.Context(), linked)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, ".git"); r.CommonDir != want {
			t.Errorf("CommonDir = %q, want %q (the shared git dir)", r.CommonDir, want)
		}
		if r.Root != root {
			t.Errorf("Root = %q, want %q (the main checkout)", r.Root, root)
		}
	})

	t.Run("submodule checkout resolves to its own repo", func(t *testing.T) {
		super := gittest.Repo(t, filepath.Join(work, "super"))
		gittest.Repo(t, filepath.Join(work, "sub"))
		gittest.Run(t, super, "-c", "protocol.file.allow=always",
			"submodule", "add", "-q", "../sub", "lib")
		r, err := Find(t.Context(), filepath.Join(super, "lib"))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(super, ".git", "modules", "lib"); r.CommonDir != want {
			t.Errorf("CommonDir = %q, want %q", r.CommonDir, want)
		}
		if want := filepath.Join(super, "lib"); r.Root != want {
			t.Errorf("Root = %q, want %q (the submodule checkout)", r.Root, want)
		}
	})

	t.Run("separate-git-dir checkout resolves its main worktree", func(t *testing.T) {
		gitDir := filepath.Join(work, "app.git")
		app := filepath.Join(work, "app")
		gittest.Run(t, work, "init", "-q", "-b", "main", "--separate-git-dir", gitDir, app)
		gittest.Run(t, app, "commit", "-q", "--allow-empty", "-m", "initial")
		r, err := Find(t.Context(), app)
		if err != nil {
			t.Fatal(err)
		}
		if r.CommonDir != gitDir {
			t.Errorf("CommonDir = %q, want %q", r.CommonDir, gitDir)
		}
		if r.Root != app {
			t.Errorf("Root = %q, want %q (the main checkout)", r.Root, app)
		}
	})

	t.Run("bare repository is refused", func(t *testing.T) {
		bare := filepath.Join(work, "bare.git")
		if err := os.MkdirAll(bare, 0o755); err != nil {
			t.Fatal(err)
		}
		gittest.Run(t, bare, "init", "-q", "--bare")
		_, err := Find(t.Context(), bare)
		if err == nil || !strings.Contains(err.Error(), "bare") {
			t.Errorf("Find(bare) error = %v, want a bare-repository explanation", err)
		}
	})

	t.Run("outside any repository", func(t *testing.T) {
		outside := filepath.Join(work, "elsewhere")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Find(t.Context(), outside)
		var notRepo *NotARepoError
		if !errors.As(err, &notRepo) {
			t.Fatalf("Find() error = %v, want NotARepoError", err)
		}
		if got := notRepo.ExitCode(); got != 4 {
			t.Errorf("ExitCode() = %d, want 4 (D13: not a wt repo)", got)
		}
	})
}

func TestSlugAndStateDir(t *testing.T) {
	gittest.Scrub(t)
	work := gittest.TempDir(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(work, "state"))

	find := func(t *testing.T, dir string) *Repo {
		t.Helper()
		r, err := Find(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := find(t, gittest.Repo(t, filepath.Join(work, "one", "acme")))
	b := find(t, gittest.Repo(t, filepath.Join(work, "two", "acme")))

	slugPattern := regexp.MustCompile(`^acme-[0-9a-f]{8}$`)
	if !slugPattern.MatchString(a.Slug()) {
		t.Errorf("Slug() = %q, want to match %v", a.Slug(), slugPattern)
	}
	if a.Slug() == b.Slug() {
		t.Errorf("same-basename repos share slug %q; the hash must split them", a.Slug())
	}

	dir, err := a.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(work, "state", "wt", "repos", a.Slug())
	if dir != want {
		t.Errorf("StateDir() = %q, want %q", dir, want)
	}
}

func TestStateDirFallsBackToHome(t *testing.T) {
	gittest.Scrub(t)
	work := gittest.TempDir(t)
	t.Setenv("XDG_STATE_HOME", "")
	r, err := Find(t.Context(), gittest.Repo(t, filepath.Join(work, "acme")))
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := r.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "wt", "repos", r.Slug()); dir != want {
		t.Errorf("StateDir() = %q, want %q", dir, want)
	}
}

func TestConfigPath(t *testing.T) {
	gittest.Scrub(t)
	root := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "acme"))
	r, err := Find(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".git", "wt.toml"); r.ConfigPath() != want {
		t.Errorf("ConfigPath() = %q, want %q", r.ConfigPath(), want)
	}
}

func TestTreesDir(t *testing.T) {
	gittest.Scrub(t)
	work := gittest.TempDir(t)
	root := gittest.Repo(t, filepath.Join(work, "acme"))
	r, err := Find(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{"default is a sibling container", "", filepath.Join(work, "acme.trees")},
		{
			"relative resolves against the main checkout",
			"../custom.trees",
			filepath.Join(work, "custom.trees"),
		},
		{"absolute is kept", filepath.Join(work, "abs.trees"), filepath.Join(work, "abs.trees")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.TreesDir(tt.configured); got != tt.want {
				t.Errorf("TreesDir(%q) = %q, want %q", tt.configured, got, tt.want)
			}
		})
	}
}

func TestSanitizeBranch(t *testing.T) {
	tests := []struct{ branch, want string }{
		{"login", "login"},
		{"feature/login", "feature-login"},
		{"a/b/c", "a-b-c"},
	}
	for _, tt := range tests {
		if got := SanitizeBranch(tt.branch); got != tt.want {
			t.Errorf("SanitizeBranch(%q) = %q, want %q", tt.branch, got, tt.want)
		}
	}
}
