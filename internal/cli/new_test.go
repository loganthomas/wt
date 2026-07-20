package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/loganthomas/wt/internal/gittest"
)

// A wrapping git hook exports GIT_DIR and friends; a setup hook
// that runs git itself must still act on its own tree.
func TestRunHookScrubsRepoLocalEnv(t *testing.T) {
	gittest.Scrub(t)
	hookRepo := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "hook-repo"))
	target := gittest.Repo(t, filepath.Join(gittest.TempDir(t), "target"))
	t.Setenv("GIT_DIR", filepath.Join(hookRepo, ".git"))
	t.Setenv("GIT_WORK_TREE", hookRepo)

	var out strings.Builder
	if err := runHook(t.Context(), target, "git rev-parse --show-toplevel", &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != target {
		t.Errorf("hook saw toplevel %q, want %q despite GIT_DIR pointing elsewhere", got, target)
	}
}
