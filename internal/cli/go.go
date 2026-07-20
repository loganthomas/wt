package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/nav"
)

func newGoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "go [query]",
		Short: "Fuzzy-jump to a tree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJump(cmd, nameArg(args))
		},
	}
}

// runJump is the one navigation path behind bare `wt`, bare
// `wt go`, and `wt go <query>`: it ends with a single tree path
// on stdout, which the shell shim turns into a cd (D11).
func runJump(cmd *cobra.Command, query string) error {
	trees, err := repoTrees(cmd.Context())
	if err != nil {
		return err
	}
	if query == "" {
		return jumpInteractive(cmd, trees)
	}
	winner, contenders := nav.Resolve(jumpCandidates(trees), query)
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
		return fmt.Errorf("no tree matches %q — `wt ls` shows what exists", query)
	}
}

// jumpInteractive opens the picker when a human is present;
// otherwise it degrades to the porcelain listing so scripts and
// agents never hang on a TUI (D12, R15).
func jumpInteractive(cmd *cobra.Command, trees []gitx.Worktree) error {
	if !interactive() {
		_, err := fmt.Fprint(cmd.OutOrStdout(), formatPorcelain(trees))
		return err
	}
	choice, err := pickTree(cmd.Context(), jumpCandidates(trees))
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), choice.Path)
	return nil
}

// jumpCandidates converts worktrees into jump targets.
// Bare entries are dropped: they have no checkout to land in.
func jumpCandidates(trees []gitx.Worktree) []nav.Candidate {
	cands := make([]nav.Candidate, 0, len(trees))
	for _, t := range trees {
		if t.Bare {
			continue
		}
		cands = append(cands, nav.Candidate{Branch: t.Branch, Path: t.Path})
	}
	return cands
}
