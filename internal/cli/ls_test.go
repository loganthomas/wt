package cli

import (
	"strings"
	"testing"

	"github.com/loganthomas/wt/internal/gitx"
)

func TestFormatRowsKeepsStateAlignedAcrossStatelessRows(t *testing.T) {
	rows := formatRows([]gitx.Worktree{
		{Branch: "main", Path: "/short", Locked: true},
		{Branch: "feature/login", Path: "/a/plain/tree"},
		{Branch: "fix", Path: "/a/much/longer/path", Locked: true},
	})
	lines := strings.Split(strings.TrimSuffix(rows, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("formatRows() = %d lines, want 3:\n%s", len(lines), rows)
	}
	first, last := strings.Index(lines[0], "locked"), strings.Index(lines[2], "locked")
	if first == -1 || first != last {
		t.Errorf("state columns misaligned (offsets %d and %d):\n%s", first, last, rows)
	}
	for _, line := range lines {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("trailing whitespace in %q", line)
		}
	}
}

func TestFormatRowsJoinsMultipleStates(t *testing.T) {
	rows := formatRows([]gitx.Worktree{
		{Branch: "old", Path: "/gone", Locked: true, Prunable: true},
	})
	if !strings.Contains(rows, "locked,prunable") {
		t.Errorf("formatRows() = %q, want a locked,prunable state cell", rows)
	}
}
