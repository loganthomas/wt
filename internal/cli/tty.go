package cli

import (
	"os"

	"golang.org/x/term"
)

// interactive reports whether wt may open a TUI.
// Stdin and stderr are the probes — deliberately not stdout: the
// shell shim captures stdout to implement the cd protocol, so a
// TTY there can never be required, and the picker itself renders
// on /dev/tty. Scripts and agents run with piped or redirected
// streams and fall through to porcelain output instead (D12, R15).
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}
