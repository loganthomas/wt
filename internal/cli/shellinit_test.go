package cli

import (
	"bytes"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files")

var shellInitVariants = []struct {
	name   string
	golden string
	args   []string
}{
	{"plain", "shell-init.zsh", []string{"shell-init", "zsh"}},
	{"prompt", "shell-init-prompt.zsh", []string{"shell-init", "zsh", "--prompt"}},
}

func TestShellInitMatchesGolden(t *testing.T) {
	for _, tt := range shellInitVariants {
		t.Run(tt.name, func(t *testing.T) {
			got := execute(t, tt.args...)
			golden := filepath.Join("testdata", "golden", tt.golden)
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(string(want), got); diff != "" {
				t.Errorf("shell-init output drifted from %s (-want +got):\n%s"+
					"\nrun `go test ./internal/cli -update` if the change is intended", golden, diff)
			}
		})
	}
}

// The emitted script must parse before it can be trusted in
// anyone's .zshrc; zsh -n is the cheapest honest check.
func TestShellInitEmitsValidZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not on PATH")
	}
	for _, tt := range shellInitVariants {
		t.Run(tt.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "shim.zsh")
			if err := os.WriteFile(script, []byte(execute(t, tt.args...)), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(zsh, "-n", script).CombinedOutput(); err != nil {
				t.Errorf("zsh -n rejected the emitted script: %v\n%s", err, out)
			}
		})
	}
}

func TestShellInitRejectsOtherShells(t *testing.T) {
	root := newRootCmd(BuildInfo{})
	root.SetArgs([]string{"shell-init", "bash"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		t.Fatal("shell-init bash succeeded, want a usage error")
	}
	if got := exitCodeFor(err); got != 2 {
		t.Errorf("exitCodeFor(%v) = %d, want 2", err, got)
	}
}

// execute runs the wt command tree in-process and returns stdout.
func execute(t *testing.T, args ...string) string {
	t.Helper()
	root := newRootCmd(BuildInfo{})
	root.SetArgs(args)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("wt %v: %v", args, err)
	}
	return out.String()
}
