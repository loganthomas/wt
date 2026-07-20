package cli

import (
	"os"

	"golang.org/x/term"
)

// interactive reports whether wt may open a TUI.
// Stdin and stderr are the probes, deliberately not stdout: the
// shell shim captures stdout to implement the cd protocol, so a
// TTY there can never be required, and the picker itself renders
// on /dev/tty. Scripts and agents run with piped or redirected
// streams and fall through to porcelain output instead (D12, R15).
//
// A dumb or unset TERM (emacs M-x shell, some CI PTYs) would pass
// the TTY probes and then hard-fail the picker's terminfo lookup;
// degrading to porcelain keeps the fallback graceful there too.
func interactive() bool {
	if t := os.Getenv("TERM"); t == "" || t == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}
