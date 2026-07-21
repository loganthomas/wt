package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/nav"
	"github.com/loganthomas/wt/internal/pool"
	"github.com/loganthomas/wt/internal/repo"
)

func newGoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "go [query]",
		Short: "Fuzzy-jump to a tree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An explicitly empty query must not alias the bare
			// form: `cd "$(wt go "$Q")"` with Q unset would get
			// exit 0 and a listing instead of a loud failure.
			if len(args) == 1 && args[0] == "" {
				return usageError{errors.New("empty query — drop the argument for the picker")}
			}
			return runJump(cmd, nameArg(args))
		},
	}
}

// runJump is the one navigation path behind bare `wt`, bare
// `wt go`, and `wt go <query>`: it ends with a single tree path
// on stdout, which the shell shim turns into a cd (D11).
func runJump(cmd *cobra.Command, query string) error {
	r, trees, err := repoTrees(cmd.Context())
	if err != nil {
		return err
	}
	cands := jumpCandidates(trees, slotTreesDir(r))
	if query == "" {
		return jumpInteractive(cmd, trees, cands)
	}
	winner, contenders := nav.Resolve(cands, query)
	switch {
	case winner != nil:
		fmt.Fprintln(cmd.OutOrStdout(), winner.Path)
		return nil
	case len(contenders) > 0:
		chatter := cmd.ErrOrStderr()
		fmt.Fprintf(chatter, "closest matches for %q:\n", query)
		for _, c := range contenders {
			fmt.Fprintf(chatter, "  %-24s %s\n", c.Display(), c.Path)
		}
		return preconditionf("%q is ambiguous — narrow the query, or run bare `wt` to pick", query)
	default:
		return errNoTreeMatches(query)
	}
}

// jumpInteractive opens the picker when a human is present;
// otherwise it degrades to the porcelain listing so scripts and
// agents never hang on a TUI (D12, R15).
func jumpInteractive(cmd *cobra.Command, trees []gitx.Worktree, cands []nav.Candidate) error {
	if !interactive() {
		_, err := fmt.Fprint(cmd.OutOrStdout(), formatPorcelain(trees))
		return err
	}
	choice, err := pickTree(cmd.Context(), cands)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), choice.Path)
	return nil
}

// jumpCandidates converts worktrees into jump targets.
// Bare entries are dropped: they have no checkout to land in.
// Parked slots are dropped too — there is nothing to work on in
// one, and landing there would race the next claim's reset —
// while claimed slots carry their address for display
// ("branch → pool-3", PLAN.md Phase 4).
func jumpCandidates(trees []gitx.Worktree, treesDir string) []nav.Candidate {
	cands := make([]nav.Candidate, 0, len(trees))
	for _, t := range trees {
		if t.Bare {
			continue
		}
		slot, isSlot := "", false
		if treesDir != "" {
			slot, isSlot = pool.SlotPath(treesDir, t.Path)
		}
		if isSlot && t.Branch == "" {
			continue
		}
		cands = append(cands, nav.Candidate{Branch: t.Branch, Path: t.Path, Slot: slot})
	}
	return cands
}

// slotTreesDir reports the container to annotate slots from, or
// "" when pool mode is off — and on config trouble, which only
// costs the annotation, never the jump, matching repoTrees'
// broken-config tolerance.
func slotTreesDir(r *repo.Repo) string {
	cfg, err := loadMerged(r)
	if err != nil || cfg.Pool == nil {
		return ""
	}
	return r.TreesDir(cfg.TreesDir)
}
