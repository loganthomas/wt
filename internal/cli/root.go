// Package cli wires the wt command tree.
//
// Contract (see PLAN.md D13): paths and porcelain data go to stdout,
// human chatter goes to stderr, and exit codes are 0 ok, 1 error,
// 2 usage, 3 precondition failed, 4 not a repo.
package cli

import (
	"cmp"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const (
	exitErr          = 1
	exitUsage        = 2
	exitPrecondition = 3
)

// exitCoder is the single seam mapping errors to process exit codes.
// Codes 3 (precondition failed) and 4 (not a wt repo) arrive as
// errors that implement it; Main never grows special cases.
// The wt-specific method name means only deliberate implementers
// match: a plain ExitCode() would let foreign types in error
// chains (exec.ExitError, via os.ProcessState) satisfy the seam
// by accident and smuggle git, hook, and editor exit codes
// through the contract.
type exitCoder interface {
	WtExitCode() int
}

// BuildInfo identifies the binary.
// The zero value reads as a development build;
// release values are injected in cmd/wt via ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// String renders the version line shown by wt --version.
func (b BuildInfo) String() string {
	return fmt.Sprintf("%s (commit %s, built %s)",
		cmp.Or(b.Version, "dev"),
		cmp.Or(b.Commit, "none"),
		cmp.Or(b.Date, "unknown"))
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
		return coded.WtExitCode()
	}
	return exitErr
}

func newRootCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:     "wt",
		Short:   "A thin, elegant wrapper around git worktree",
		Version: info.String(),
		Args:    cobra.NoArgs,
		RunE:    runRoot,
		// Errors are reported once by Main, with wt's exit-code mapping.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(wrapFlagError)
	root.AddCommand(
		newInitCmd(),
		newNewCmd(),
		newLsCmd(),
		newGoCmd(),
		newDoneCmd(),
		newPathCmd(),
		newConfigCmd(),
	)
	// Argument validators are wrapped centrally so bad arguments
	// exit 2 (D13) on every command, present and future:
	// the contract is structural, not a per-command ritual.
	wrapUsageArgs(root)
	return root
}

// wrapUsageArgs walks the whole command tree, nested subcommands
// included, so no future command escapes the exit-2 contract.
// A nil validator is left alone: it is cobra's subcommand routing.
func wrapUsageArgs(cmd *cobra.Command) {
	if cmd.Args != nil {
		cmd.Args = usageArgs(cmd.Args)
	}
	for _, sub := range cmd.Commands() {
		wrapUsageArgs(sub)
	}
}

// runRoot handles bare `wt`: the most frequent intent is
// "take me to a tree", so the picker is the default (D12).
// Help stays one `wt -h` away.
func runRoot(cmd *cobra.Command, _ []string) error {
	return runJump(cmd, "")
}

func wrapFlagError(_ *cobra.Command, err error) error {
	return usageError{err}
}

// usageError marks errors that exit 2 per D13's machine-output contract.
type usageError struct{ err error }

func (u usageError) Error() string   { return u.err.Error() }
func (u usageError) Unwrap() error   { return u.err }
func (u usageError) WtExitCode() int { return exitUsage }

// preconditionError reports a blocked-but-recoverable state
// (exit 3 per D13): the command was understood but the repo
// isn't in a state where it can run.
type preconditionError struct{ msg string }

func (e preconditionError) Error() string   { return e.msg }
func (e preconditionError) WtExitCode() int { return exitPrecondition }

func preconditionf(format string, args ...any) error {
	return preconditionError{fmt.Sprintf(format, args...)}
}

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
