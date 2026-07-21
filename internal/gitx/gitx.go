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
	"os"
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

// RevParse runs git rev-parse with the given flags and returns
// one output line per flag that prints something.
func (g *Git) RevParse(ctx context.Context, flags ...string) ([]string, error) {
	out, err := g.runLine(ctx, append([]string{"rev-parse"}, flags...)...)
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
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

// WorktreeRemoveForce removes the worktree at path even when it
// holds untracked or ignored files. Only for trees wt's own
// guards have already vouched for: pool slots keep gitignored
// warm caches that git would otherwise refuse to remove.
func (g *Git) WorktreeRemoveForce(ctx context.Context, path string) error {
	_, err := g.run(ctx, "worktree", "remove", "--force", path)
	return err
}

// WorktreeAddDetach creates a worktree at path with a detached
// HEAD at ref, the parked state of a pool slot.
func (g *Git) WorktreeAddDetach(ctx context.Context, path, ref string) error {
	_, err := g.run(ctx, "worktree", "add", "--quiet", "--detach", path, ref)
	return err
}

// CheckoutDetach forcibly detaches HEAD at ref, discarding local
// modifications to tracked files. This is the destructive half of
// a pool slot reset; callers run the pattern and orphan guards
// first (D14).
func (g *Git) CheckoutDetach(ctx context.Context, ref string) error {
	_, err := g.run(ctx, "checkout", "--quiet", "--force", "--detach", ref)
	return err
}

// CleanUntracked removes untracked files and directories.
// Never -x: gitignored build artifacts are what keep pool slots
// warm (D14), so ignored files always survive a reset.
// Double -f because a single one skips untracked nested git repos
// while still exiting 0; a reset that reported success would
// leave them to fail the next holder's guards, with no wt command
// able to clear them.
func (g *Git) CleanUntracked(ctx context.Context) error {
	_, err := g.run(ctx, "clean", "-q", "-ffd")
	return err
}

// Switch checks out an existing branch.
func (g *Git) Switch(ctx context.Context, branch string) error {
	_, err := g.run(ctx, "switch", "--quiet", branch)
	return err
}

// SwitchCreate creates branch at the current HEAD and checks it
// out.
func (g *Git) SwitchCreate(ctx context.Context, branch string) error {
	_, err := g.run(ctx, "switch", "--quiet", "-c", branch)
	return err
}

// DeleteBranch deletes a local branch even if unmerged;
// callers run the unpushed-commit guard first.
func (g *Git) DeleteBranch(ctx context.Context, branch string) error {
	_, err := g.run(ctx, "branch", "-q", "-D", branch)
	return err
}

// ShortStatus returns `git status -sb` as git prints it:
// the branch line plus one line per change.
// It exists for the picker's preview pane, so the text is for
// human eyes and is never parsed.
func (g *Git) ShortStatus(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "status", "-sb")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// LastCommit returns a one-line summary of the tree's HEAD.
func (g *Git) LastCommit(ctx context.Context) (string, error) {
	return g.runLine(ctx, "log", "-1", "--format=%h %s (%cr)")
}

// StatusEntry is one line of `git status --porcelain -z` output.
type StatusEntry struct {
	Code string // two-character XY status, e.g. "??", " M"
	Path string // repo-root-relative, forward slashes
}

// Status lists the worktree's staged, unstaged, and untracked
// changes; empty means clean.
// -uall names every untracked file individually; the default
// collapses untracked directories to "dir/", which would hide
// files callers need to match by exact path.
func (g *Git) Status(ctx context.Context) ([]StatusEntry, error) {
	out, err := g.run(ctx, "status", "--porcelain", "-z", "-uall")
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
		// Either column can hold the R/C: the index side ("R ")
		// or the worktree side (" R", e.g. mv plus add -N).
		if strings.ContainsAny(code, "RC") {
			i++
		}
	}
	return entries, nil
}

// Tracked reports which of the given paths (relative to g's
// directory) are tracked in the index.
func (g *Git) Tracked(ctx context.Context, paths ...string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, err := g.run(ctx, append([]string{"ls-files", "-z", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool)
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			tracked[p] = true
		}
	}
	return tracked, nil
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

// ValidBranchName reports whether git accepts name for a branch.
func (g *Git) ValidBranchName(ctx context.Context, name string) bool {
	_, err := g.run(ctx, "check-ref-format", "--branch", name)
	return err == nil
}

// localRepoEnv mirrors `git rev-parse --local-env-vars`: the
// variables git exports to hooks to pin its own repository.
// When wt itself runs under a hook it inherits them, and left in
// place they would silently retarget every command at the hook's
// repo instead of cmd.Dir, guards included, so `wt done` could
// pass its checks against the wrong tree and destroy work.
var localRepoEnv = map[string]bool{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR":                   true,
	"GIT_CONFIG":                       true,
	"GIT_CONFIG_COUNT":                 true,
	"GIT_CONFIG_PARAMETERS":            true,
	"GIT_DIR":                          true,
	"GIT_GRAFT_FILE":                   true,
	"GIT_IMPLICIT_WORK_TREE":           true,
	"GIT_INDEX_FILE":                   true,
	"GIT_INTERNAL_SUPER_PREFIX":        true,
	"GIT_NO_REPLACE_OBJECTS":           true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_PREFIX":                       true,
	"GIT_REPLACE_REF_BASE":             true,
	"GIT_SHALLOW_FILE":                 true,
	"GIT_WORK_TREE":                    true,
}

// ScrubbedEnv is the process environment minus the repo-local
// git variables. The GIT_CONFIG_KEY_n/VALUE_n pairs travel with
// GIT_CONFIG_COUNT and are matched by prefix.
// Exported for spawns of non-git programs that may call git
// themselves (setup hooks): they need the same protection.
func ScrubbedEnv() []string {
	environ := os.Environ()
	// The spare slot is for run's LC_ALL=C append.
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if localRepoEnv[name] ||
			strings.HasPrefix(name, "GIT_CONFIG_KEY_") ||
			strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	return env
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
	// Pinned to the C locale: callers classify git's stderr text
	// (e.g. mapping "not a git repository" to exit 4), and localized
	// messages would silently break that mapping.
	cmd.Env = append(ScrubbedEnv(), "LC_ALL=C")
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
