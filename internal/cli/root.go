// Package cli wires the wt command tree.
//
// Contract (see PLAN.md D13): paths and porcelain data go to stdout,
// human chatter goes to stderr,
// and exit codes are 0 ok, 1 error, 2 usage.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// BuildInfo identifies the binary.
// The zero value reads as a development build;
// release values are injected in cmd/wt via ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func (b BuildInfo) String() string {
	if b.Version == "" {
		b.Version = "dev"
	}
	return fmt.Sprintf("%s (commit %s, built %s)", b.Version, b.Commit, b.Date)
}

// Main runs the wt CLI and returns its process exit code.
func Main(info BuildInfo) int {
	root := newRootCmd(info)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		if isUsageError(err) {
			return 2
		}
		return 1
	}
	return 0
}

func newRootCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:     "wt",
		Short:   "A thin, elegant wrapper around git worktree",
		Version: info.String(),
		// Errors are reported once by Main, with wt's exit-code mapping.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError{err}
	})
	return root
}

// usageError marks errors that must exit 2 per the machine-output contract.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

func isUsageError(err error) bool {
	_, ok := err.(usageError)
	return ok
}
