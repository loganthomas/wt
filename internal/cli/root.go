// Package cli wires the wt command tree.
//
// Contract (see PLAN.md D13): paths and porcelain data go to stdout,
// human chatter goes to stderr,
// and exit codes are 0 ok, 1 error, 2 usage.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const (
	exitErr   = 1
	exitUsage = 2
)

// exitCoder is the single seam mapping errors to process exit codes.
// Later phases add codes 3 (precondition failed) and 4 (not a wt repo)
// by returning errors that implement it — Main never grows special cases.
type exitCoder interface {
	ExitCode() int
}

// BuildInfo identifies the binary.
// The zero value reads as a development build;
// release values are injected in cmd/wt via ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func (b BuildInfo) String() string {
	return fmt.Sprintf("%s (commit %s, built %s)",
		orDefault(b.Version, "dev"),
		orDefault(b.Commit, "none"),
		orDefault(b.Date, "unknown"))
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Main runs the wt CLI and returns its process exit code.
func Main(info BuildInfo) int {
	root := newRootCmd(info)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		return exitCodeFor(err)
	}
	return 0
}

func exitCodeFor(err error) int {
	var coded exitCoder
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return exitErr
}

func newRootCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:     "wt",
		Short:   "A thin, elegant wrapper around git worktree",
		Version: info.String(),
		// Wrapped so cobra's unknown-command error exits 2, not 1.
		Args: usageArgs(cobra.NoArgs),
		RunE: runRoot,
		// Errors are reported once by Main, with wt's exit-code mapping.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(wrapFlagError)
	root.AddCommand(newLsCmd())
	return root
}

// runRoot handles bare `wt`: help for now, the fuzzy picker in Phase 3.
func runRoot(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func wrapFlagError(_ *cobra.Command, err error) error {
	return usageError{err}
}

// usageError marks errors that exit 2 per D13's machine-output contract.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }
func (u usageError) ExitCode() int { return exitUsage }

// usageArgs wraps a cobra positional-args validator
// so its failures carry the usage exit code.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}
