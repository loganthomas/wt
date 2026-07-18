package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFragment(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeFragmentsDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, fragmentsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBatchGroupsTypesAndConsumesFragments(t *testing.T) {
	root := t.TempDir()
	frags := makeFragmentsDir(t, root)
	writeTestFragment(t, frags, "2.bug.md", "`wt ls` no longer panics on bare repos. (#2)")
	writeTestFragment(t, frags, "1.enh.md", "`wt ls` lists every worktree. (#1)")
	writeTestFragment(t, frags, "README.md", "see CONTRIBUTING.md")

	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatalf("batchChangelog() error: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, changelogFile))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Changelog\n\n" +
		"## v0.1.0-alpha.1 - 2026-07-18\n\n" +
		"### Enhancements\n\n- `wt ls` lists every worktree. (#1)\n\n" +
		"### Fixes\n\n- `wt ls` no longer panics on bare repos. (#2)\n"
	if string(raw) != want {
		t.Errorf("CHANGELOG.md = %q, want %q", raw, want)
	}

	entries, err := os.ReadDir(frags)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "README.md" {
		t.Errorf("fragments dir = %v, want only README.md left", entries)
	}
}

func TestFragmentsSortByPRNumberWithinAType(t *testing.T) {
	root := t.TempDir()
	frags := makeFragmentsDir(t, root)
	writeTestFragment(t, frags, "10.enh.md", "PR ten. (#10)")
	writeTestFragment(t, frags, "2.enh.md", "PR two. (#2)")

	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, changelogFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	two, ten := strings.Index(text, "PR two. (#2)"), strings.Index(text, "PR ten. (#10)")
	if two == -1 || ten == -1 || two > ten {
		t.Errorf("expected PR 2 before PR 10:\n%s", text)
	}
}

func TestBatchFailsOnStrayFragments(t *testing.T) {
	root := t.TempDir()
	frags := makeFragmentsDir(t, root)
	writeTestFragment(t, frags, "40.feature.md", "Mistyped type. (#40)")
	writeTestFragment(t, frags, "41.enh.md", "Valid. (#41)")

	err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18")
	if err == nil || !strings.Contains(err.Error(), "40.feature.md") {
		t.Fatalf("batchChangelog() error = %v, want a stray-fragment error naming the file", err)
	}
	if _, statErr := os.Stat(filepath.Join(frags, "41.enh.md")); statErr != nil {
		t.Error("batchChangelog() consumed fragments despite failing on a stray")
	}
}

func TestBatchPrependsAboveOlderSections(t *testing.T) {
	root := t.TempDir()
	frags := makeFragmentsDir(t, root)
	writeTestFragment(t, frags, "3.doc.md", "Recipes page added. (#3)")
	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatal(err)
	}
	writeTestFragment(t, frags, "4.enh.md", "`wt new` creates worktrees. (#4)")
	if err := batchChangelog(root, "v0.1.0-alpha.2", "2026-08-01"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, changelogFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	newer := strings.Index(text, "## v0.1.0-alpha.2")
	older := strings.Index(text, "## v0.1.0-alpha.1")
	if newer == -1 || older == -1 || newer > older {
		t.Errorf("expected alpha.2 section above alpha.1:\n%s", text)
	}
}

func TestBatchRefusesDuplicateVersion(t *testing.T) {
	root := t.TempDir()
	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatal(err)
	}
	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-19"); err == nil {
		t.Error("batchChangelog() accepted a duplicate version")
	}
}

func TestBatchWithoutFragmentsWritesPlaceholder(t *testing.T) {
	root := t.TempDir()
	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatalf("batchChangelog() error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, changelogFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "No notable changes.") {
		t.Errorf("CHANGELOG.md = %q, want a no-notable-changes placeholder", raw)
	}
}

func TestExtractReturnsOnlyTheRequestedSection(t *testing.T) {
	root := t.TempDir()
	frags := makeFragmentsDir(t, root)
	writeTestFragment(t, frags, "5.enh.md", "First. (#5)")
	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatal(err)
	}
	writeTestFragment(t, frags, "6.bug.md", "Second. (#6)")
	if err := batchChangelog(root, "v0.1.0-alpha.2", "2026-08-01"); err != nil {
		t.Fatal(err)
	}

	section, err := extractSection(root, "v0.1.0-alpha.1")
	if err != nil {
		t.Fatalf("extractSection() error: %v", err)
	}
	if !strings.Contains(section, "First. (#5)") || strings.Contains(section, "Second. (#6)") {
		t.Errorf("extractSection() = %q, want only the alpha.1 body", section)
	}
	if strings.Contains(section, "## v0.1.0-alpha.1") {
		t.Errorf("extractSection() = %q, want the heading stripped", section)
	}
}

func TestExtractFailsWhenVersionMissing(t *testing.T) {
	root := t.TempDir()
	if err := batchChangelog(root, "v0.1.0-alpha.1", "2026-07-18"); err != nil {
		t.Fatal(err)
	}
	if _, err := extractSection(root, "v0.1.0-alpha.9"); err == nil {
		t.Error("extractSection() succeeded for a version that was never batched")
	}
}

func TestWriteFragmentValidatesAndRefusesDuplicates(t *testing.T) {
	dir := t.TempDir()
	path, err := writeFragment(dir, 7, "enh", "Adds a thing.")
	if err != nil {
		t.Fatalf("writeFragment() error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), "Adds a thing. (#7)\n"; got != want {
		t.Errorf("fragment = %q, want %q", got, want)
	}

	if _, err := writeFragment(dir, 7, "enh", "Again."); err == nil {
		t.Error("writeFragment() overwrote an existing fragment")
	}
	if _, err := writeFragment(dir, 8, "nope", "Bad type."); err == nil {
		t.Error("writeFragment() accepted an unknown type")
	}
	if _, err := writeFragment(dir, 0, "enh", "Bad PR."); err == nil {
		t.Error("writeFragment() accepted PR number 0")
	}
	if _, err := writeFragment(dir, 9, "enh", "   "); err == nil {
		t.Error("writeFragment() accepted a blank message")
	}
	if _, err := writeFragment(dir, 10, "enh", "Line one.\nLine two."); err == nil {
		t.Error("writeFragment() accepted a multi-line message")
	}
}

func TestPromptKeepsAnswerDeliveredWithEOF(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("final answer"))
	got, err := prompt(in, "label")
	if err != nil {
		t.Fatalf("prompt() error: %v", err)
	}
	if got != "final answer" {
		t.Errorf("prompt() = %q, want %q", got, "final answer")
	}

	empty := bufio.NewReader(strings.NewReader(""))
	if _, err := prompt(empty, "label"); err == nil {
		t.Error("prompt() succeeded on empty input")
	}
}
