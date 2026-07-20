package gitx

import (
	"path/filepath"
	"testing"

	"github.com/loganthomas/wt/internal/gittest"
)

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
