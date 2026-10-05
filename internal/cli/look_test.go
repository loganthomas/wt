package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/loganthomas/wt/internal/gitx"
)

// goldenLooks covers what a human can be shown: a wide color
// terminal, the same output with color off (NO_COLOR, pipes,
// ui.color = "never"), and a narrow terminal that forces paths
// to truncate and doctor text to wrap.
var goldenLooks = []struct {
	name string
	look look
}{
	{"color-120", look{color: true, width: 120, home: "/Users/me"}},
	{"plain-120", look{width: 120, home: "/Users/me"}},
	{"plain-60", look{width: 60, home: "/Users/me"}},
}

func TestHumanOutputMatchesGolden(t *testing.T) {
	kb := int64(54 * 1024)
	clean, dirty := false, true
	trees := []gitx.Worktree{
		{Branch: "main", Path: "/Users/me/src/acme", Head: "82506bc30aa1"},
		{
			Branch: "feature/login", Path: "/Users/me/src/acme.trees/feature-login",
			Head: "a08fe1242bb2", Locked: true,
		},
		{
			Branch: "fix/a-rather-long-branch-name", Head: "422825f71cc3",
			Path: "/Users/me/src/acme.trees/fix-a-rather-long-branch-name-for-a-long-path",
		},
		{Branch: "old", Path: "/Users/me/src/acme.trees/old", Head: "4a53648b9dd4", Prunable: true},
	}
	facts := map[string]treeFacts{
		"/Users/me/src/acme":                     {Dirty: &clean, Age: "4d"},
		"/Users/me/src/acme.trees/feature-login": {Dirty: &dirty, Age: "2h", Tools: "stale"},
		"/Users/me/src/acme.trees/fix-a-rather-long-branch-name-for-a-long-path": {
			Dirty: &clean, Age: "now", Tools: "fresh",
		},
	}
	status := statusView{
		Mode: "pool",
		Base: baseView{Name: "main", Stale: true},
		Trees: []treeStatus{
			{Branch: "main", Path: "/Users/me/src/acme", DiskKB: &kb},
			{Detached: true, Path: "/Users/me/src/acme.trees/slot-1"},
		},
		Pool: &poolStatus{Size: 4, Slots: []slotView{
			{
				Slot: "slot-1", State: "free", Path: "/Users/me/src/acme.trees/slot-1",
				Head: "878e4ca68ee5", Tools: "fresh",
			},
			{
				Slot: "slot-2", State: "claimed", Branch: "feature/pay", Note: "pid 4242",
				Age: "4d", Path: "/Users/me/src/acme.trees/slot-2",
				Head: "a08fe1242bb2", Dirty: true, Tools: "stale",
			},
			{
				Slot: "slot-3", State: "stale", Branch: "spike",
				Note: "dead pid 7", Path: "/Users/me/src/acme.trees/slot-3",
				Head: "422825f71cc3", Tools: "fresh",
			},
			{
				Slot: "slot-4", State: "unprovisioned", Note: "provisions on first claim",
				Path: "/Users/me/src/acme.trees/slot-4",
			},
		}},
	}
	doctor := doctorView{Issues: 1, Checks: []checkResult{
		{Name: "git", Status: statusOK, Symptom: "2.50.1"},
		{
			Name: "hooks-path", Status: statusWarn,
			Symptom: "core.hooksPath = .githooks (relative)",
			Cause: "a relative hooks path resolves inside each tree; " +
				"hooks vanish in trees where it is untracked",
			Fix: "`git config core.hooksPath <absolute path>`, or keep the hooks directory tracked",
		},
		{
			Name: "config", Status: statusFail,
			Symptom: "wt.toml:3:1: unknown key \"colour\"",
			Fix:     "wt config --edit",
		},
		{Name: "update", Status: statusInfo, Symptom: "check skipped (--offline)"},
	}}

	for _, gl := range goldenLooks {
		for name, got := range map[string]string{
			"ls":     formatRows(trees, facts, gl.look),
			"status": formatStatus(status, gl.look),
			"doctor": formatDoctor(doctor, gl.look),
		} {
			t.Run(name+"-"+gl.name, func(t *testing.T) {
				checkGolden(t, filepath.Join("look", name+"-"+gl.name+".txt"), got)
				checkFitsWidth(t, got, gl.look.width)
			})
		}
	}
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}

// checkFitsWidth holds every line inside the terminal, except the
// one documented overflow: a table whose fixed columns alone are
// wider than the terminal, where FitColumn stops at its minimum.
func checkFitsWidth(t *testing.T, got string, width int) {
	t.Helper()
	for line := range strings.Lines(got) {
		line = strings.TrimSuffix(line, "\n")
		if strings.HasSuffix(line, " ") {
			t.Errorf("trailing whitespace: %q", line)
		}
		if w := ansi.StringWidth(line); width >= 120 && w > width {
			t.Errorf("line is %d cells wide, terminal is %d: %q", w, width, line)
		}
	}
}

func TestPlainLookEmitsNoEscapes(t *testing.T) {
	got := formatDoctor(doctorView{Checks: []checkResult{
		{Name: "git", Status: statusFail, Symptom: "too old", Cause: "c", Fix: "f"},
	}}, look{width: 40})
	if strings.Contains(got, "\x1b") {
		t.Errorf("plain look emitted an escape sequence: %q", got)
	}
}

func TestUseColor(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		env     []string
		want    bool
	}{
		{"always beats a pipe", "always", nil, true},
		{"never beats a forced terminal", "never", []string{"CLICOLOR_FORCE=1"}, false},
		{"auto on a pipe is plain", "auto", []string{"TERM=xterm-256color"}, false},
		{"auto honors CLICOLOR_FORCE", "auto", []string{"TERM=xterm-256color", "CLICOLOR_FORCE=1"}, true},
		{
			"NO_COLOR beats CLICOLOR_FORCE", "auto",
			[]string{"TERM=xterm-256color", "CLICOLOR_FORCE=1", "NO_COLOR=1"},
			false,
		},
		{"NO_COLOR counts any value", "auto", []string{"CLICOLOR_FORCE=1", "NO_COLOR=yes"}, false},
		{"empty NO_COLOR is unset", "auto", []string{"CLICOLOR_FORCE=1", "NO_COLOR="}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := useColor(&bytes.Buffer{}, tt.setting, tt.env); got != tt.want {
				t.Errorf("useColor(%q, %q) = %v, want %v", tt.setting, tt.env, got, tt.want)
			}
		})
	}
}
