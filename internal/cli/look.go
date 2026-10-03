package cli

import (
	"io"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"

	"github.com/loganthomas/wt/internal/config"
)

// look is how human output should appear where it is going:
// whether to color it, and how many columns the terminal has
// (0 means unlimited: never truncate or wrap).
type look struct {
	color bool
	width int
}

// plainLook is output for pipes and files: no color, full width,
// byte-identical to what scripts have always parsed.
var plainLook = look{}

// Color carries meaning only (status, never decoration), in the
// 16 basic ANSI colors, so it follows the user's terminal theme.
var (
	styleGood = lipgloss.NewStyle().Foreground(lipgloss.Green)
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleBad  = lipgloss.NewStyle().Foreground(lipgloss.Red)
	styleDim  = lipgloss.NewStyle().Faint(true)
	styleBold = lipgloss.NewStyle().Bold(true)
)

// paint styles text when this look has color; empty text stays
// empty so Align can still drop trailing blank cells.
func (l look) paint(s lipgloss.Style, text string) string {
	if !l.color || text == "" {
		return text
	}
	return s.Render(text)
}

// lookFor resolves the look for one output stream. Only a real
// terminal gets a width: piped output is never truncated.
func lookFor(w io.Writer) look {
	f, ok := w.(*os.File)
	if !ok {
		return plainLook
	}
	l := look{color: useColor(f, colorSetting(), os.Environ())}
	if isTerminal(f) {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil {
			l.width = width
		}
	}
	return l
}

// useColor applies ui.color: "always" and "never" are absolute,
// and "auto" defers to colorprofile for CLICOLOR_FORCE, TERM=dumb,
// and whether w is a terminal. NO_COLOR is checked here, to the
// no-color.org letter: colorprofile skips it on non-terminals, so
// CLICOLOR_FORCE would win, and only counts boolean values.
func useColor(w io.Writer, setting string, env []string) bool {
	switch setting {
	case "always":
		return true
	case "never":
		return false
	}
	if slices.ContainsFunc(env, isNoColorSet) {
		return false
	}
	return colorprofile.Detect(w, env) >= colorprofile.ANSI
}

func isNoColorSet(kv string) bool {
	value, ok := strings.CutPrefix(kv, "NO_COLOR=")
	return ok && value != ""
}

// colorSetting reads ui.color from the global config, the only
// file it may live in. Any trouble reads as "auto": a broken
// config is for the commands that load it to report, and color
// must never be the reason a command fails.
func colorSetting() string {
	path, err := config.GlobalPath()
	if err != nil {
		return "auto"
	}
	ui, err := config.LoadUI(path)
	if err != nil || ui.Color == "" {
		return "auto"
	}
	return ui.Color
}
