package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestAlignMeasuresDisplayWidthNotEscapes(t *testing.T) {
	red := "\x1b[31mlocked\x1b[m"
	got := Align([][]string{
		{red, "/a"},
		{"locked", "/b"},
	})
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if ansi.StringWidth(lines[0]) != ansi.StringWidth(lines[1]) {
		t.Errorf("colored and plain rows misalign:\n%q\n%q", lines[0], lines[1])
	}
}

func TestFitColumnUnlimitedIsUntouched(t *testing.T) {
	rows := [][]string{{"main", strings.Repeat("x", 200)}}
	if got := FitColumn(rows, 1, 0); got[0][1] != rows[0][1] {
		t.Errorf("FitColumn(width 0) changed the cell: %q", got[0][1])
	}
}

func TestFitColumnTruncatesMiddleToFit(t *testing.T) {
	path := "/Users/me/src/acme.trees/fix-a-rather-long-branch-name"
	rows := [][]string{
		{"fix", path, "prunable"},
		{"main", "/Users/me/src/acme", ""},
	}
	got := FitColumn(rows, 1, 50)
	cell := got[0][1]
	if !strings.Contains(cell, "…") {
		t.Fatalf("long path not truncated: %q", cell)
	}
	if !strings.HasPrefix(cell, "/Users") || !strings.HasSuffix(cell, "branch-name") {
		t.Errorf("truncation lost the path's ends: %q", cell)
	}
	if got[1][1] != "/Users/me/src/acme" {
		t.Errorf("a path that fits was changed: %q", got[1][1])
	}
	for _, line := range strings.Split(strings.TrimSuffix(Align(got), "\n"), "\n") {
		if w := ansi.StringWidth(line); w > 50 {
			t.Errorf("line is %d cells, want ≤ 50: %q", w, line)
		}
	}
	if rows[0][1] != path {
		t.Errorf("FitColumn mutated its input: %q", rows[0][1])
	}
}

func TestFitColumnLeavesHopelessTablesAlone(t *testing.T) {
	// The other columns alone overflow 20 cells: truncating the
	// path can't make the row fit, so it must not lose characters.
	rows := [][]string{{"a-very-long-branch-name", "/some/path/here/x", "prunable"}}
	if got := FitColumn(rows, 1, 20); got[0][1] != rows[0][1] {
		t.Errorf("FitColumn truncated a table that cannot fit: %q", got[0][1])
	}
}
