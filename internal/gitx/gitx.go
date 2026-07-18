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
