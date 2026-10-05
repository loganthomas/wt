package cli

import (
	"cmp"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"

	"github.com/loganthomas/wt/internal/config"
)

// look is how human output should appear where it is going:
// whether to color it, how many columns the terminal has
// (0 means unlimited: never truncate or wrap), and the home
// directory to abbreviate as ~ ("" keeps paths whole).
type look struct {
	color bool
	width int
	home  string
}

// plainLook is the barest human output: no color, full width,
// full paths.
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

// header renders a table's column titles: uppercase and dim,
// so they frame the data without competing with it.
func (l look) header(titles ...string) []string {
	row := make([]string, len(titles))
	for i, t := range titles {
		row[i] = l.paint(styleDim, strings.ToUpper(t))
	}
	return row
}

// path abbreviates the home directory as ~ for human tables;
// machine output always carries the absolute path.
func (l look) path(p string) string {
	if l.home == "" {
		return p
	}
	if p == l.home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, l.home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return p
}

// dash fills an unknown or empty cell, so a column never reads
// as missing.
func dash(s string) string {
	return cmp.Or(s, "-")
}

// shortHead is the abbreviated commit for a HEAD column.
func shortHead(sha string) string {
	const n = 9
	if len(sha) <= n {
		return dash(sha)
	}
	return sha[:n]
}

// lookFor resolves the look for one output stream. Only a real
// terminal gets a width: piped output is never truncated.
func lookFor(w io.Writer) look {
	f, ok := w.(*os.File)
	if !ok {
		return plainLook
	}
	l := look{color: useColor(f, colorSetting(), os.Environ())}
	if home, err := os.UserHomeDir(); err == nil {
		l.home = home
	}
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
