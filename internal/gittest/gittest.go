// Package gittest builds throwaway git repositories for unit tests.
//
// Its environment is scrubbed of the caller's GIT_* variables:
// tests also run under githooks (the repo's pre-commit gate),
// where git exports things like GIT_INDEX_FILE that would
// otherwise leak into the fixture repos and corrupt them.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TempDir returns a fresh, symlink-resolved temp directory,
// so its paths compare equal with git's physical-path output
// (macOS's /var/folders is symlinked).
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Repo creates a git repository at dir (made if needed)
// with branch main and one empty commit, and returns dir.
func Repo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Run(t, dir, "init", "-q", "-b", "main")
	Run(t, dir, "commit", "-q", "--allow-empty", "-m", "initial")
	return dir
}

// Run executes git in dir and returns its trimmed stdout,
// failing the test on any error.
func Run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = string(exit.Stderr)
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// WriteFile writes content under the repository at dir.
func WriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Scrub removes the caller's GIT_* variables from the test
// process itself, restoring them when the test ends.
// Tests that drive wt's production code (which inherits the
// process environment) need this; fixtures run via Run are
// already scrubbed by Env.
func Scrub(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if name, value, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "GIT_") {
			// Setenv registers restoration on cleanup and
			// rejects parallel tests, which the unset below
			// would race with.
			t.Setenv(name, value)
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// BaseEnv is the isolation recipe every wt test environment
// shares — no system config, a fixed identity — as KEY=VALUE
// pairs. The testscript harness applies the same pairs so unit
// tests and .txtar scripts can never drift apart.
func BaseEnv() []string {
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=wt-test",
		"GIT_AUTHOR_EMAIL=wt-test@example.invalid",
		"GIT_COMMITTER_NAME=wt-test",
		"GIT_COMMITTER_EMAIL=wt-test@example.invalid",
	}
}

// Env is the scrubbed process environment plus BaseEnv;
// suitable for exec.Cmd.Env when a test runs git-adjacent binaries.
func Env() []string {
	env := BaseEnv()
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}
