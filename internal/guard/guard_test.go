package guard

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loganthomas/wt/internal/gittest"
)

func TestCheckDirty(t *testing.T) {
	gittest.Scrub(t)
	tests := []struct {
		name  string
		state func(t *testing.T, dir string)
		want  bool // want a violation
	}{
		{"clean tree passes", func(t *testing.T, dir string) {}, false},
		{"untracked file blocks", func(t *testing.T, dir string) {
			gittest.WriteFile(t, dir, "scratch.txt", "wip")
		}, true},
		{"modified file blocks", func(t *testing.T, dir string) {
			gittest.WriteFile(t, dir, "tracked.txt", "v2")
		}, true},
		{"staged file blocks", func(t *testing.T, dir string) {
			gittest.WriteFile(t, dir, "staged.txt", "new")
			gittest.Run(t, dir, "add", "staged.txt")
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "acme"))
			gittest.WriteFile(t, dir, "tracked.txt", "v1")
			gittest.Run(t, dir, "add", "tracked.txt")
			gittest.Run(t, dir, "commit", "-q", "-m", "track a file")
			tt.state(t, dir)

			err := CheckDirty(t.Context(), dir)
			assertViolation(t, err, tt.want, "uncommitted")
		})
	}
}

func TestCheckDirtyTolerated(t *testing.T) {
	gittest.Scrub(t)
	dir := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "acme"))
	gittest.WriteFile(t, dir, ".env", "SECRET=1")

	if err := CheckDirty(t.Context(), dir, ".env"); err != nil {
		t.Errorf("CheckDirty(tolerate .env) = %v, want nil", err)
	}
	assertViolation(t, CheckDirty(t.Context(), dir), true, "uncommitted")

	// Tolerance covers only untracked files: the same name staged
	// is real work in flight.
	gittest.Run(t, dir, "add", "-f", ".env")
	assertViolation(t, CheckDirty(t.Context(), dir, ".env"), true, "uncommitted")
}

func TestCheckUnpushed(t *testing.T) {
	gittest.Scrub(t)

	// setup returns a worktree on branch "feature" in the given state.
	setup := func(t *testing.T, withOrigin bool) (root, tree string) {
		t.Helper()
		work := gittest.TempDir(t)
		root = gittest.Repo(t, filepath.Join(work, "acme"))
		if withOrigin {
			origin := filepath.Join(work, "origin.git")
			gittest.Run(t, root, "init", "-q", "--bare", origin)
			gittest.Run(t, root, "remote", "add", "origin", origin)
			gittest.Run(t, root, "push", "-q", "-u", "origin", "main")
		}
		tree = filepath.Join(work, "acme.trees", "feature")
		gittest.Run(t, root, "worktree", "add", "-q", tree, "-b", "feature")
		return root, tree
	}
	commit := func(t *testing.T, tree, msg string) {
		t.Helper()
		gittest.Run(t, tree, "commit", "-q", "--allow-empty", "-m", msg)
	}

	t.Run("branch fully pushed passes", func(t *testing.T) {
		_, tree := setup(t, true)
		commit(t, tree, "work")
		gittest.Run(t, tree, "push", "-q", "-u", "origin", "feature")
		if err := CheckUnpushed(t.Context(), tree, "main"); err != nil {
			t.Errorf("CheckUnpushed() = %v, want nil", err)
		}
	})

	t.Run("commit beyond the remote blocks", func(t *testing.T) {
		_, tree := setup(t, true)
		commit(t, tree, "pushed")
		gittest.Run(t, tree, "push", "-q", "-u", "origin", "feature")
		commit(t, tree, "not pushed")
		assertViolation(t, CheckUnpushed(t.Context(), tree, "main"), true, "neither pushed nor merged")
	})

	t.Run("merged into local base passes without any remote", func(t *testing.T) {
		root, tree := setup(t, false)
		commit(t, tree, "work")
		gittest.Run(t, root, "merge", "-q", "--ff-only", "feature")
		if err := CheckUnpushed(t.Context(), tree, "main"); err != nil {
			t.Errorf("CheckUnpushed() = %v, want nil", err)
		}
	})

	t.Run("unmerged commit without any remote blocks", func(t *testing.T) {
		_, tree := setup(t, false)
		commit(t, tree, "work")
		assertViolation(t, CheckUnpushed(t.Context(), tree, "main"), true, "neither pushed nor merged")
	})

	t.Run("missing base ref is tolerated", func(t *testing.T) {
		_, tree := setup(t, true)
		gittest.Run(t, tree, "push", "-q", "-u", "origin", "feature")
		if err := CheckUnpushed(t.Context(), tree, "no-such-branch"); err != nil {
			t.Errorf("CheckUnpushed() = %v, want nil", err)
		}
	})
}

func TestCheckOrphans(t *testing.T) {
	gittest.Scrub(t)

	t.Run("on a branch passes", func(t *testing.T) {
		dir := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "acme"))
		if err := CheckOrphans(t.Context(), dir); err != nil {
			t.Errorf("CheckOrphans() = %v, want nil", err)
		}
	})

	t.Run("detached at a branch tip passes", func(t *testing.T) {
		dir := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "acme"))
		gittest.Run(t, dir, "checkout", "-q", "--detach", "main")
		if err := CheckOrphans(t.Context(), dir); err != nil {
			t.Errorf("CheckOrphans() = %v, want nil", err)
		}
	})

	t.Run("detached with unreachable commits blocks", func(t *testing.T) {
		dir := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "acme"))
		gittest.Run(t, dir, "checkout", "-q", "--detach", "main")
		gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "orphan work")
		err := CheckOrphans(t.Context(), dir)
		assertViolation(t, err, true, "reachable only from")
		// The recovery hint must hand the user a working rescue command.
		if !strings.Contains(err.Error(), "git branch") {
			t.Errorf("error %q should include a git branch rescue hint", err)
		}
	})
}

func TestViolationExitCode(t *testing.T) {
	v := &Error{Tree: "/t", Reason: "r", Hint: "h"}
	if got := v.ExitCode(); got != 3 {
		t.Errorf("ExitCode() = %d, want 3 (D13: precondition failed)", got)
	}
}

// assertViolation checks err against want: when want is true,
// err must be a *Violation mentioning wantSubstr.
func assertViolation(t *testing.T, err error, want bool, wantSubstr string) {
	t.Helper()
	if !want {
		if err != nil {
			t.Errorf("got %v, want no violation", err)
		}
		return
	}
	var v *Error
	if !errors.As(err, &v) {
		t.Fatalf("got %v (%T), want *guard.Error", err, err)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("violation %q should mention %q", err, wantSubstr)
	}
}
