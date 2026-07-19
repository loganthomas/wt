// Package repo resolves a repository's identity: its shared git dir,
// its main checkout, and the wt paths derived from them
// (per-repo config, state dir, trees container — see PLAN.md D4, D14).
package repo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/loganthomas/wt/internal/gitx"
)

// Repo identifies one repository, however many worktrees it has.
type Repo struct {
	// CommonDir is the shared .git directory, absolute.
	// Every linked worktree resolves to the same one,
	// which is what makes it wt's config and identity anchor.
	CommonDir string
	// Root is the main checkout's top-level directory.
	Root string
}

// Find resolves the repository containing dir.
func Find(ctx context.Context, dir string) (*Repo, error) {
	g := gitx.New(dir)
	lines, err := g.RevParse(ctx, "--path-format=absolute", "--git-common-dir",
		"--is-bare-repository")
	if err != nil {
		var gitErr *gitx.Error
		if errors.As(err, &gitErr) && strings.Contains(gitErr.Stderr, "not a git repository") {
			return nil, &NotARepoError{Dir: describeDir(dir)}
		}
		return nil, err
	}
	if len(lines) != 2 {
		return nil, fmt.Errorf("unexpected git rev-parse output: %q", lines)
	}
	common, bare := lines[0], lines[1] == "true"
	if bare {
		return nil, fmt.Errorf(
			"%s is a bare repository; wt needs a main checkout to anchor config and trees", common)
	}
	if filepath.Base(common) == ".git" {
		return &Repo{CommonDir: common, Root: filepath.Dir(common)}, nil
	}
	// A common dir not named .git means an indirected layout —
	// a submodule (.git/modules/<name>) or --separate-git-dir.
	// The parent-dir rule doesn't hold there, and git itself only
	// knows the checkout from inside it: when this worktree IS the
	// main one (its git dir is the common dir), its top level is
	// the root being looked for.
	lines, err = g.RevParse(ctx, "--path-format=absolute", "--git-dir", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	if len(lines) == 2 && lines[0] == common {
		return &Repo{CommonDir: common, Root: lines[1]}, nil
	}
	return nil, fmt.Errorf(
		"cannot locate the main checkout for %s — run wt from the main worktree", common)
}

// Slug names this repo's state directory: <basename>-<hash8>.
// The basename keeps it human-readable,
// the hash of the common dir keeps same-named repos apart.
func (r *Repo) Slug() string {
	sum := sha256.Sum256([]byte(r.CommonDir))
	return fmt.Sprintf("%s-%x", filepath.Base(r.Root), sum[:4])
}

// ConfigPath is the per-repo config file inside the shared git dir,
// where every worktree sees it and `git clean` can never touch it (D4).
func (r *Repo) ConfigPath() string {
	return filepath.Join(r.CommonDir, "wt.toml")
}

// StateDir is where wt keeps this repo's leases, hashes, and
// timestamps, honoring $XDG_STATE_HOME with the ~/.local/state
// fallback. The directory is not created here.
func (r *Repo) StateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "wt", "repos", r.Slug()), nil
}

// TreesDir resolves the container directory that holds all
// wt-managed trees. An empty configured value means the default
// sibling `../<repo>.trees`; a relative value is anchored at the
// main checkout so it means the same thing from any worktree.
func (r *Repo) TreesDir(configured string) string {
	if configured == "" {
		return r.Root + ".trees"
	}
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured)
	}
	return filepath.Join(r.Root, configured)
}

// describeDir names the searched directory for error messages,
// resolving the empty "current directory" convention to a real
// path so the user sees where the search actually happened.
func describeDir(dir string) string {
	if dir != "" {
		return dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "the current directory"
}

// NotARepoError reports a directory outside any git repository.
type NotARepoError struct {
	Dir string
}

func (e *NotARepoError) Error() string {
	return fmt.Sprintf("%s is not inside a git repository", e.Dir)
}

// ExitCode maps to D13's "not a wt repo" contract code.
func (e *NotARepoError) ExitCode() int { return 4 }

// SanitizeBranch converts a branch name into its tree directory
// name: slashes flatten to dashes so `feature/login` nests no
// deeper than the trees container (D14).
func SanitizeBranch(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}
