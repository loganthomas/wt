package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/repo"
)

type initOptions struct {
	base     string
	treesDir string
	poolSize int
	copyList []string
	yes      bool
}

func newInitCmd() *cobra.Command {
	var opts initOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up wt for this repository",
		Long: "Set up wt for this repository: asks a few questions\n" +
			"(or takes flags with --yes) and writes .git/wt.toml.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.base, "base", "", "base branch new trees start from")
	f.StringVar(&opts.treesDir, "trees-dir", "", "container directory for wt-managed trees")
	f.IntVar(&opts.poolSize, "pool-size", 0, "pre-warmed pool slots (0 keeps pool mode off)")
	f.StringSliceVar(&opts.copyList, "copy", nil, "untracked files to copy into new trees")
	f.BoolVar(&opts.yes, "yes", false, "no prompts; use defaults for anything not flagged")
	return cmd
}

func runInit(cmd *cobra.Command, opts initOptions) error {
	// Rejected rather than folded into "pool off": a negative
	// value is a typo, and 0 already spells "no pool" on purpose.
	if opts.poolSize < 0 {
		return usageError{fmt.Errorf("--pool-size cannot be negative, got %d", opts.poolSize)}
	}
	ctx := cmd.Context()
	r, err := repo.Find(ctx, "")
	if err != nil {
		return err
	}
	if _, err := os.Stat(r.ConfigPath()); err == nil {
		return preconditionf(
			"wt is already set up here (%s) — edit it with `wt config --edit`", r.ConfigPath())
	}

	// Global defaults (and the built-ins behind them) seed the
	// form, so flags only need to state what differs.
	seed, err := loadMerged(r)
	if err != nil {
		return err
	}
	opts.base = cmp.Or(opts.base, seed.Base)
	opts.treesDir = cmp.Or(opts.treesDir, seed.TreesDir, r.DefaultTreesDir())
	if opts.copyList == nil {
		opts.copyList = seed.Copy
	}

	if !opts.yes {
		if !isTerminal(os.Stdin) {
			return usageError{errors.New(
				"stdin is not a terminal; run `wt init --yes` with value flags instead")}
		}
		if err := runInitForm(&opts); err != nil {
			return err
		}
	}

	cfg := config.Config{
		Base:     opts.base,
		TreesDir: opts.treesDir,
		Copy:     opts.copyList,
	}
	if opts.poolSize > 0 {
		cfg.Pool = &config.Pool{Size: opts.poolSize}
	}
	if err := config.Save(r.ConfigPath(), cfg); err != nil {
		return err
	}

	mode := "default mode"
	if cfg.Pool != nil {
		mode = fmt.Sprintf("pool mode, %d slots", cfg.Pool.Size)
	}
	chatter := cmd.ErrOrStderr()
	fmt.Fprintf(chatter, "initialized wt (%s) — config at %s\n", mode, r.ConfigPath())
	if cfg.Pool != nil {
		return provisionInitialPool(ctx, r, chatter)
	}
	return nil
}

// provisionInitialPool pre-warms the just-configured pool.
// The merged config is reloaded first: hooks and copy lists may
// come from the global layer, and provisioning must see exactly
// what a later claim will. A base that doesn't resolve yet only
// defers the work — claims provision missing slots on demand.
func provisionInitialPool(ctx context.Context, r *repo.Repo, chatter io.Writer) error {
	merged, err := loadMerged(r)
	if err != nil {
		return err
	}
	p, err := poolOf(&wtRepo{repo: r, cfg: merged})
	if err != nil {
		return err
	}
	if !p.g.HasCommit(ctx, merged.Base) {
		fmt.Fprintf(chatter,
			"base %q not found — slots will be provisioned on first claim\n", merged.Base)
		return nil
	}
	return resizeHeld(p.provisionPool(ctx, 0, merged.Pool.Size, chatter))
}

// runInitForm collects the same values the flags cover,
// pre-filled with whatever the flags and defaults already chose.
func runInitForm(opts *initOptions) error {
	copyStr := strings.Join(opts.copyList, ",")
	usePool := opts.poolSize > 0
	sizeStr := "4"
	if usePool {
		sizeStr = strconv.Itoa(opts.poolSize)
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Base branch").
				Description("New trees branch off this").
				Validate(huh.ValidateNotEmpty()).
				Value(&opts.base),
			huh.NewInput().
				Title("Trees directory").
				Description("Container for wt-managed trees, relative to the main checkout").
				Validate(huh.ValidateNotEmpty()).
				Value(&opts.treesDir),
			huh.NewInput().
				Title("Copy into new trees").
				Description("Untracked files new trees need, comma-separated (e.g. .env,.envrc)").
				Value(&copyStr),
			huh.NewConfirm().
				Title("Pool mode?").
				Description("Pre-warmed, reusable slots — for big repos where cold trees are unusable").
				Value(&usePool),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Pool size").
				Description("Number of pre-warmed slots").
				Validate(validatePoolSize).
				Value(&sizeStr),
		).WithHideFunc(func() bool { return !usePool }),
	)
	if err := form.Run(); err != nil {
		return err
	}

	opts.copyList = splitList(copyStr)
	opts.poolSize = 0
	if usePool {
		// Validated by the form; Atoi cannot fail here.
		opts.poolSize, _ = strconv.Atoi(strings.TrimSpace(sizeStr))
	}
	return nil
}

func validatePoolSize(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return errors.New("enter a whole number of at least 1")
	}
	return nil
}

// splitList parses a comma-separated form answer, dropping blanks.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
