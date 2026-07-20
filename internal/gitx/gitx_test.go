package gitx

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/loganthomas/wt/internal/gittest"
)

// A worktree-column rename (" R") carries an origin path just
// like an index-column one; the parser must not surface the
// origin as a bogus entry of its own.
func TestStatusSkipsWorktreeRenameOrigin(t *testing.T) {
	gittest.Scrub(t)
	dir := gittest.Repo(t, gittest.TempDir(t))
	gittest.WriteFile(t, dir, "a.txt", "hi\n")
	gittest.Run(t, dir, "add", "a.txt")
	gittest.Run(t, dir, "commit", "-q", "-m", "add")
	if err := os.Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "add", "-N", "b.txt")

	entries, err := New(dir).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []StatusEntry{{Code: " R", Path: "b.txt"}}
	if !slices.Equal(entries, want) {
		t.Errorf("Status() = %v, want %v", entries, want)
	}
}

// A hook environment exports GIT_DIR and friends for its own
// repo; wt commands anchored elsewhere must not be retargeted.
func TestRunScrubsRepoLocalEnv(t *testing.T) {
	gittest.Scrub(t)
	hookRepo := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "hook-repo"))
	target := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "target"))
	t.Setenv("GIT_DIR", filepath.Join(hookRepo, ".git"))
	t.Setenv("GIT_WORK_TREE", hookRepo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(hookRepo, ".git", "index"))

	got, err := New(target).TopLevel(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("TopLevel() = %q, want %q despite GIT_DIR pointing elsewhere", got, target)
	}
}
