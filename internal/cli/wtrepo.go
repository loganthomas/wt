package cli

import (
	"context"
	"path/filepath"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/repo"
	"github.com/loganthomas/wt/internal/state"
)

// wtRepo bundles what nearly every command needs:
// the resolved repository and its merged configuration.
type wtRepo struct {
	repo *repo.Repo
	cfg  config.Config
}

// openRepo resolves the repository around the working directory
// and loads its merged config.
func openRepo(ctx context.Context) (*wtRepo, error) {
	r, err := repo.Find(ctx, "")
	if err != nil {
		return nil, err
	}
	cfg, err := loadMerged(r)
	if err != nil {
		return nil, err
	}
	return &wtRepo{repo: r, cfg: cfg}, nil
}

// loadMerged is the one place the config layers come together for
// an already-found repo; every command that needs config goes
// through it so they can never disagree on the effective values.
func loadMerged(r *repo.Repo) (config.Config, error) {
	globalPath, err := config.GlobalPath()
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(globalPath, r.ConfigPath())
}

// treesDir is the resolved container for this repo's managed trees.
func (w *wtRepo) treesDir() string {
	return w.repo.TreesDir(w.cfg.TreesDir)
}

// treeStateName maps a worktree path to its recorded-state key.
// Only trees directly inside the trees dir have state — keyed by
// basename — so a hand-made worktree elsewhere must never touch a
// managed namesake's records.
func (w *wtRepo) treeStateName(path string) (string, bool) {
	if filepath.Dir(path) != w.treesDir() {
		return "", false
	}
	return filepath.Base(path), true
}

// stateDir is this repo's state root: leases, refresh hashes.
func (w *wtRepo) stateDir() (state.Dir, error) {
	sd, err := w.repo.StateDir()
	if err != nil {
		return "", err
	}
	return state.Dir(sd), nil
}

// repoTrees resolves the repository and lists its worktrees,
// deliberately without loading config: read-only commands like
// ls and path must keep working even when wt.toml is broken.
// Resolving the repo first keeps the contract's exit 4 for
// non-repos, and anchors the listing at the same root every
// other command uses.
func repoTrees(ctx context.Context) (*repo.Repo, []gitx.Worktree, error) {
	r, err := repo.Find(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	trees, err := gitx.New(r.Root).Worktrees(ctx)
	return r, trees, err
}
