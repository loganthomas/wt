package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/config"
	"github.com/loganthomas/wt/internal/repo"
)

func newConfigCmd() *cobra.Command {
	var edit bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show the active config paths and merged values",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if edit {
				return runConfigEdit(cmd)
			}
			return runConfig(cmd)
		},
	}
	cmd.Flags().BoolVar(&edit, "edit", false, "open the repo config in $EDITOR")
	return cmd
}

func runConfig(cmd *cobra.Command) error {
	w, err := openRepo(cmd.Context())
	if err != nil {
		return err
	}
	globalPath, err := config.GlobalPath()
	if err != nil {
		return err
	}

	// The effective trees dir is resolved before rendering:
	// the reader should see the directory wt will actually use,
	// not the shorthand it was configured with.
	cfg := w.cfg
	cfg.TreesDir = w.treesDir()
	rendered, err := config.Render(cfg)
	if err != nil {
		return err
	}

	// Paths ride along as TOML comments, keeping stdout one
	// well-formed document for humans and parsers alike (D13).
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# repo:   %s%s\n", w.repo.ConfigPath(), missingNote(w.repo.ConfigPath()))
	fmt.Fprintf(out, "# global: %s%s\n\n", globalPath, missingNote(globalPath))
	fmt.Fprint(out, rendered)
	return nil
}

func missingNote(path string) string {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return " (missing)"
	}
	return ""
}

func runConfigEdit(cmd *cobra.Command) error {
	ctx := cmd.Context()
	r, err := repo.Find(ctx, "")
	if err != nil {
		return err
	}
	path := r.ConfigPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return preconditionf("no repo config at %s yet — run `wt init` first", path)
	}

	editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	// Run through the shell so multi-word editors ("code --wait")
	// keep working; the path rides as $1 to dodge quoting games.
	edit := exec.CommandContext(ctx, "sh", "-c", editor+` "$1"`, "sh", path)
	edit.Stdin = os.Stdin
	edit.Stdout = os.Stderr
	edit.Stderr = os.Stderr
	if err := edit.Run(); err != nil {
		return fmt.Errorf("editor %q: %w", editor, err)
	}

	// Validate right away: a typo surfaces now, at the terminal
	// where it can be fixed, not on some later command.
	globalPath, err := config.GlobalPath()
	if err != nil {
		return err
	}
	if _, err := config.Load(globalPath, path); err != nil {
		return fmt.Errorf("saved, but the config does not load: %w", err)
	}
	return nil
}
