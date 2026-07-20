package cli

import (
	_ "embed"
	"fmt"
	"text/template"

	"github.com/spf13/cobra"
)

//go:embed shim.zsh.tmpl
var shimSource string

// shimTemplate is parsed once at startup; a broken template is a
// build defect, so parsing panics rather than returning an error.
var shimTemplate = template.Must(template.New("shim.zsh").Parse(shimSource))

func newShellInitCmd() *cobra.Command {
	var prompt bool
	cmd := &cobra.Command{
		Use:   "shell-init zsh",
		Short: "Emit the shell integration for eval in ~/.zshrc",
		Args: cobra.MatchAll(cobra.ExactArgs(1), func(_ *cobra.Command, args []string) error {
			if args[0] != "zsh" {
				return fmt.Errorf("unsupported shell %q — v1 supports zsh only", args[0])
			}
			return nil
		}),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runShellInit(cmd, prompt)
		},
	}
	cmd.Flags().BoolVar(&prompt, "prompt", false, "include the WT_PROMPT indicator hook")
	return cmd
}

// runShellInit writes the shim to stdout: the script itself is
// the machine output here, consumed by eval in .zshrc (D11).
func runShellInit(cmd *cobra.Command, prompt bool) error {
	return shimTemplate.Execute(cmd.OutOrStdout(), struct{ Prompt bool }{prompt})
}
