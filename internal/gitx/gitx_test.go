package gitx

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestShortStatusReportsBranchAndChanges(t *testing.T) {
	gittest.Scrub(t)
	dir := gittest.Repo(t, gittest.TempDir(t))
	gittest.WriteFile(t, dir, "a.txt", "hi\n")

	out, err := New(dir).ShortStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## main", "?? a.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("ShortStatus() = %q, want it to contain %q", out, want)
		}
	}
}

func TestLastCommitSummarizesHead(t *testing.T) {
	gittest.Scrub(t)
	dir := gittest.Repo(t, gittest.TempDir(t))

	out, err := New(dir).LastCommit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "initial") || strings.Contains(out, "\n") {
		t.Errorf("LastCommit() = %q, want a one-line summary of the initial commit", out)
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

// TestSlotLifecycleOperations walks the git operations a pool
// slot goes through: provision detached, take a branch, reset
// back to detached, clean untracked — with ignored files (the
// warm caches pool mode exists for) surviving every reset.
func TestSlotLifecycleOperations(t *testing.T) {
	gittest.Scrub(t)
	dir := gittest.TempDir(t)
	main := gittest.Repo(t, filepath.Join(dir, "acme"))
	gittest.WriteFile(t, main, ".gitignore", "node_modules/\n")
	gittest.Run(t, main, "add", ".gitignore")
	gittest.Run(t, main, "commit", "-q", "-m", "ignore")

	slot := filepath.Join(dir, "acme.trees", "pool-1")
	g := New(main)
	if err := g.WorktreeAddDetach(t.Context(), slot, "main"); err != nil {
		t.Fatal(err)
	}
	trees, err := g.Worktrees(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 || !trees[1].Detached {
		t.Fatalf("after add --detach: trees = %+v, want a detached second tree", trees)
	}

	sg := New(slot)
	if err := sg.SwitchCreate(t.Context(), "feat"); err != nil {
		t.Fatal(err)
	}
	if out := gittest.Run(t, slot, "branch", "--show-current"); out != "feat" {
		t.Errorf("after SwitchCreate: on %q, want feat", out)
	}

	gittest.WriteFile(t, slot, "scratch.txt", "leftover\n")
	gittest.WriteFile(t, slot, "node_modules/dep.js", "cached\n")
	gittest.Repo(t, filepath.Join(slot, "vendored"))
	if err := sg.CheckoutDetach(t.Context(), "main"); err != nil {
		t.Fatal(err)
	}
	if err := sg.CleanUntracked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(slot, "scratch.txt")); err == nil {
		t.Error("CleanUntracked left the untracked file behind")
	}
	// A single -f skips nested git repos yet exits 0; the reset
	// must actually discard them or the next holder inherits dirt
	// no guard lets them release.
	if _, err := os.Stat(filepath.Join(slot, "vendored")); err == nil {
		t.Error("CleanUntracked left a nested git repo behind")
	}
	// Never -x: gitignored caches are what keep slots warm (D14).
	if _, err := os.Stat(filepath.Join(slot, "node_modules", "dep.js")); err != nil {
		t.Error("CleanUntracked removed a gitignored file — slots would always be cold")
	}
	if out := gittest.Run(t, slot, "branch", "--show-current"); out != "" {
		t.Errorf("after CheckoutDetach: still on branch %q", out)
	}

	if err := sg.Switch(t.Context(), "feat"); err != nil {
		t.Fatal(err)
	}
	if out := gittest.Run(t, slot, "branch", "--show-current"); out != "feat" {
		t.Errorf("after Switch: on %q, want feat", out)
	}

	// Removing a worktree holding ignored files needs force;
	// wt's own guards have already vouched for it by then.
	if err := sg.CheckoutDetach(t.Context(), "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.WorktreeRemoveForce(t.Context(), slot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(slot); err == nil {
		t.Error("WorktreeRemoveForce left the slot directory behind")
	}
}
