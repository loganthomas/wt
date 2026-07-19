// Package gitx executes the user's real git binary and parses its
// porcelain output. wt never links a git library (see PLAN.md D2):
// the git CLI is the compatibility target, and shelling out keeps
// the user's config, credentials, and hooks behaving exactly as
// they expect.
package gitx

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Git runs git commands rooted at a fixed working directory.
// An empty dir means the current process working directory.
type Git struct {
	dir string
}

// New returns a Git that runs commands from dir.
func New(dir string) *Git {
	return &Git{dir: dir}
}

// Worktrees lists every worktree of the repository containing g's directory.
func (g *Git) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := g.run(ctx, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out)
}

// CommonDir returns the absolute path of the repository's shared
// .git directory; linked worktrees resolve to the main one.
func (g *Git) CommonDir(ctx context.Context) (string, error) {
	return g.runLine(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// TopLevel returns the absolute root of the worktree containing
// g's directory.
func (g *Git) TopLevel(ctx context.Context) (string, error) {
	return g.runLine(ctx, "rev-parse", "--show-toplevel")
}

// WorktreeAdd creates a worktree at path on new branch off base.
func (g *Git) WorktreeAdd(ctx context.Context, path, branch, base string) error {
	_, err := g.run(ctx, "worktree", "add", "--quiet", path, "-b", branch, base)
	return err
}

// WorktreeRemove removes the worktree at path.
// Callers are responsible for the safety guards; git itself still
// refuses trees it considers dirty or locked.
func (g *Git) WorktreeRemove(ctx context.Context, path string) error {
	_, err := g.run(ctx, "worktree", "remove", path)
	return err
}

// DeleteBranch deletes a local branch even if unmerged;
// callers run the unpushed-commit guard first.
func (g *Git) DeleteBranch(ctx context.Context, branch string) error {
	_, err := g.run(ctx, "branch", "-q", "-D", branch)
	return err
}

// StatusEntry is one line of `git status --porcelain -z` output.
type StatusEntry struct {
	Code string // two-character XY status, e.g. "??", " M"
	Path string // repo-root-relative, forward slashes
}

// Status lists the worktree's staged, unstaged, and untracked
// changes; empty means clean.
func (g *Git) Status(ctx context.Context) ([]StatusEntry, error) {
	out, err := g.run(ctx, "status", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var entries []StatusEntry
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		code, path := f[:2], f[3:]
		entries = append(entries, StatusEntry{Code: code, Path: path})
		// Renames and copies carry the origin path as an extra
		// NUL-terminated field; it is not a record of its own.
		if code[0] == 'R' || code[0] == 'C' {
			i++
		}
	}
	return entries, nil
}

// CommitCount counts the commits selected by a rev-list spec,
// e.g. ("HEAD", "--not", "--remotes").
func (g *Git) CommitCount(ctx context.Context, spec ...string) (int, error) {
	out, err := g.runLine(ctx, append([]string{"rev-list", "--count"}, spec...)...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// HasCommit reports whether ref resolves to a commit.
func (g *Git) HasCommit(ctx context.Context, ref string) bool {
	_, err := g.run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// runLine runs git and returns its single-line output, trimmed.
func (g *Git) runLine(ctx context.Context, args ...string) (string, error) {
	out, err := g.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *Git) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		var stderr string
		if errors.As(err, &exit) {
			stderr = string(exit.Stderr)
		}
		return nil, &Error{Args: args, Stderr: stderr, Err: err}
	}
	return out, nil
}

// Error is a failed git invocation.
// Its message surfaces git's own stderr,
// which is almost always the text the user needs to see.
// Err is always non-nil: run, the only constructor,
// builds an Error solely from a failed exec.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := cmp.Or(strings.TrimSpace(e.Stderr), e.Err.Error())
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }
