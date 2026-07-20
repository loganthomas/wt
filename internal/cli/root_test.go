package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"testing"
)

func TestUsageErrorsExitTwo(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--definitely-not-a-flag"}},
		{"unknown command", []string{"bogus"}},
		{"unexpected argument", []string{"ls", "unexpected"}},
		{"unsupported shell", []string{"shell-init", "bash"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCmd(BuildInfo{})
			root.SetArgs(tt.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("Execute(%q) succeeded, want usage error", tt.args)
			}
			if got := exitCodeFor(err); got != 2 {
				t.Errorf("exitCodeFor(%v) = %d, want 2", err, got)
			}
		})
	}
}

func TestPlainErrorsExitOne(t *testing.T) {
	if got := exitCodeFor(errors.New("boom")); got != 1 {
		t.Errorf("exitCodeFor(plain error) = %d, want 1", got)
	}
}

func TestForeignExitCodesCollapseToOne(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 4")
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("Run() error = %v, want *exec.ExitError", err)
	}
	wrapped := fmt.Errorf("setup hook failed: %w", err)
	if got := exitCodeFor(wrapped); got != 1 {
		t.Errorf("exitCodeFor(hook exiting 4) = %d, want 1", got)
	}
}

func TestWrappedUsageErrorsKeepTheirExitCode(t *testing.T) {
	wrapped := fmt.Errorf("while parsing: %w", usageError{errors.New("bad flag")})
	if got := exitCodeFor(wrapped); got != 2 {
		t.Errorf("exitCodeFor(wrapped usage error) = %d, want 2", got)
	}
}
