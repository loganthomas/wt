package cli

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/repo"
)

// caskPathMarker identifies a binary the Homebrew cask installed:
// Homebrew must remove it, or its install records go stale.
const caskPathMarker = "/Caskroom/wt/"

var (
	shellInitLine = regexp.MustCompile(`\bwt shell-init\b`)
	shellSafe     = regexp.MustCompile(`^[A-Za-z0-9_./+-]+$`)
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Print the exact steps that remove wt (removes nothing itself)",
		Args:  cobra.NoArgs,
		RunE:  runUninstall,
	}
}

// uninstallStep is one numbered step of the removal plan:
// comment lines explaining it, then the commands that carry it out.
type uninstallStep struct {
	notes    []string
	commands []string
}

// runUninstall prints the removal plan and changes nothing:
// deleting config or editing an rc file stays the user's call,
// made with the real paths in front of them.
// The plan is a shell document, explanation as comments and
// commands bare, so any line on stdout can be copied as-is.
// Trees come first because finishing them safely needs wt itself.
func runUninstall(cmd *cobra.Command, _ []string) error {
	steps := []uninstallStep{
		treesStep(cmd.Context()),
		rcStep(zshrcPath()),
		binaryStep(executablePath()),
	}
	if s, ok := dataStep(); ok {
		steps = append(steps, s)
	}
	return writeSteps(cmd.OutOrStdout(), steps)
}

func writeSteps(w io.Writer, steps []uninstallStep) error {
	var b strings.Builder
	for i, s := range steps {
		if i > 0 {
			b.WriteString("\n")
		}
		for j, note := range s.notes {
			prefix := "#    "
			if j == 0 {
				prefix = fmt.Sprintf("# %d. ", i+1)
			}
			b.WriteString(prefix + note + "\n")
		}
		for _, c := range s.commands {
			b.WriteString(c + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// treesStep is best-effort about the current repo: uninstall must
// work from anywhere, so a failed lookup only drops the specifics.
func treesStep(ctx context.Context) uninstallStep {
	s := uninstallStep{notes: []string{
		"Finish your trees while wt can still check them for unpushed work:",
		"`wt ls` lists them and `wt done <name>` removes each one.",
		"Pool slots and leftovers are plain worktrees: `git worktree remove <path>`.",
	}}
	r, err := repo.Find(ctx, "")
	if err != nil || !exists(r.ConfigPath()) {
		s.notes = append(s.notes, "Each repo you ran `wt init` in keeps a .git/wt.toml; delete it there.")
		return s
	}
	s.notes = append(s.notes,
		"Each repo you ran `wt init` in keeps a .git/wt.toml, this one included:")
	s.commands = []string{"rm " + shellQuote(r.ConfigPath())}
	return s
}

// rcStep quotes the rc file's actual integration lines, so a
// customized eval (--prompt, a guard around it) is found as written.
func rcStep(rc string) uninstallStep {
	found := matchingLines(rc, shellInitLine)
	if len(found) == 0 {
		return uninstallStep{notes: []string{
			fmt.Sprintf("Delete the eval line from %s, if you added it:", rc),
			`eval "$(wt shell-init zsh)"`,
		}}
	}
	return uninstallStep{notes: append(
		[]string{fmt.Sprintf("Delete the shell integration from %s:", rc)}, found...)}
}

func zshrcPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(cmp.Or(os.Getenv("ZDOTDIR"), home), ".zshrc")
}

// matchingLines returns "line N: <text>" for each match;
// an unreadable file reads as having none.
func matchingLines(path string, pattern *regexp.Regexp) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // read-only file
	var found []string
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		if pattern.MatchString(sc.Text()) {
			found = append(found, fmt.Sprintf("line %d: %s", n, strings.TrimSpace(sc.Text())))
		}
	}
	return found
}

// executablePath returns "" when the running binary can't be located.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

func binaryStep(exe string) uninstallStep {
	if exe == "" {
		return uninstallStep{
			notes:    []string{"Remove the binary:"},
			commands: []string{`rm "$(command -v wt)"`},
		}
	}
	resolved := exe
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		resolved = r
	}
	if strings.Contains(resolved, caskPathMarker) {
		return uninstallStep{
			notes: []string{
				"Remove the binary through Homebrew, which installed it",
				"(skip the untap if you use other formulae from the tap):",
			},
			commands: []string{"brew uninstall wt", "brew untap loganthomas/tap"},
		}
	}
	return uninstallStep{
		notes:    []string{"Remove the binary:"},
		commands: []string{"rm " + shellQuote(exe)},
	}
}

// dataStep lists only directories that exist, so a fresh install
// isn't told to delete paths wt never created.
func dataStep() (uninstallStep, bool) {
	var dirs []string
	if p, err := config.GlobalPath(); err == nil {
		dirs = append(dirs, filepath.Dir(p))
	}
	if p, err := repo.StateRoot(); err == nil {
		dirs = append(dirs, p)
	}
	dirs = slices.DeleteFunc(dirs, func(d string) bool { return !exists(d) })
	if len(dirs) == 0 {
		return uninstallStep{}, false
	}
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = shellQuote(d)
	}
	return uninstallStep{
		notes:    []string{"Delete wt's global config and state (leases, fetch stamps, caches):"},
		commands: []string{"rm -rf " + strings.Join(quoted, " ")},
	}, true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// shellQuote single-quotes a path unless every character is inert
// to the shell; `~` is excluded since it would expand.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
