package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAlignPadsColumnsAcrossRows(t *testing.T) {
	got := Align([][]string{
		{"main", "/short", "locked"},
		{"feature/login", "/a/plain/tree", ""},
		{"fix", "/a/much/longer/path", "locked"},
	})
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("Align() = %d lines, want 3:\n%s", len(lines), got)
	}
	first, last := strings.Index(lines[0], "locked"), strings.Index(lines[2], "locked")
	if first == -1 || first != last {
		t.Errorf("state columns misaligned (offsets %d and %d):\n%s", first, last, got)
	}
}

func TestAlignNeverEmitsTrailingWhitespace(t *testing.T) {
	got := Align([][]string{
		{"main", "/short", ""},
		{"fix", "/a/much/longer/path", "locked"},
	})
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("trailing whitespace in %q", line)
		}
	}
}

func TestAlignPreservesCellOwnTrailingSpace(t *testing.T) {
	got := Align([][]string{{"main", "/ends/with/space "}})
	if want := "main  /ends/with/space \n"; got != want {
		t.Errorf("Align() = %q, want %q", got, want)
	}
}

func TestAlignCountsRunesNotBytes(t *testing.T) {
	got := Align([][]string{
		{"héllo", "x"},
		{"ascii", "y"},
	})
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	first := utf8.RuneCountInString(lines[0][:strings.Index(lines[0], "x")])
	second := utf8.RuneCountInString(lines[1][:strings.Index(lines[1], "y")])
	if first != second {
		t.Errorf("multibyte cell skewed the next column (rune offsets %d and %d):\n%s",
			first, second, got)
	}
}

func TestJSONEmitsIndentedObjectWithFinalNewline(t *testing.T) {
	var out strings.Builder
	err := JSON(&out, struct {
		Mode string `json:"mode"`
	}{Mode: "pool"})
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	want := "{\n  \"mode\": \"pool\"\n}\n"
	if out.String() != want {
		t.Errorf("JSON() = %q, want %q", out.String(), want)
	}
}

func TestJSONDoesNotEscapeHTMLCharacters(t *testing.T) {
	var out strings.Builder
	err := JSON(&out, struct {
		Fix string `json:"fix"`
	}{Fix: "wt pool resize <n>"})
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	if !strings.Contains(out.String(), "<n>") {
		t.Errorf("JSON() = %q, want literal <n> (no HTML escaping)", out.String())
	}
}
