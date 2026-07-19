package cli

import (
	"context"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/repo"
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
