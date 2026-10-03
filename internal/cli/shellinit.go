package cli

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// The shim ships as plain zsh in two pieces, base and opt-in
// prompt hook, so each file stays directly readable and
// checkable as zsh; --prompt simply appends the second.
//
//go:embed shim.zsh
var shimZsh string

//go:embed shim_prompt.zsh
var shimPromptZsh string

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
	out := strings.ReplaceAll(shimZsh, shimSigPlaceholder, shimSig())
	if prompt {
		out += shimPromptZsh
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), out)
	return err
}

// shimSigEnv is exported by the emitted shim; wt doctor reads it
// to tell "shim missing" from "shell predates the upgrade".
const (
	shimSigEnv         = "WT_SHIM_SIG"
	shimSigPlaceholder = "@SHIM_SIG@"
)

// shimSig fingerprints the embedded shim sources (placeholder
// form, both pieces, so a prompt-hook change counts too). Any
// edit to the shims changes it, and nothing else does.
func shimSig() string {
	sum := sha256.Sum256([]byte(shimZsh + shimPromptZsh))
	return hex.EncodeToString(sum[:])[:12]
}
