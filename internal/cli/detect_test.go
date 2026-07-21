package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func touch(t *testing.T, root, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectDefaultsEmptyRepo(t *testing.T) {
	d := detectDefaults(t.TempDir(), map[string]bool{})
	if d.marker != "" || d.refresh != "" || d.gate() != nil || d.copies != nil ||
		len(d.copyNotes()) != 0 || d.infoNotes != nil {
		t.Errorf("empty repo proposed %+v, want nothing", d)
	}
}

func TestDetectDefaultsMostSpecificLockfileWins(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "package-lock.json")
	touch(t, root, "pnpm-lock.yaml")
	d := detectDefaults(root, map[string]bool{"package-lock.json": true, "pnpm-lock.yaml": true})
	if d.refresh != "pnpm install" || !slices.Equal(d.gate(), []string{"pnpm-lock.yaml"}) {
		t.Errorf("proposed %q gated on %v, want pnpm install on its lockfile", d.refresh, d.gate())
	}
	if !strings.Contains(d.hookNote(), "pnpm-lock.yaml") {
		t.Errorf("hookNote = %q, want it to name the winning marker", d.hookNote())
	}
}

func TestDetectDefaultsCopyCandidates(t *testing.T) {
	root := t.TempDir()
	touch(t, root, ".env")
	touch(t, root, ".envrc")

	d := detectDefaults(root, map[string]bool{".envrc": true})
	if !slices.Equal(d.copies, []string{".env"}) {
		t.Errorf("copies = %v, want only the untracked .env", d.copies)
	}

	// Unknown tracking (nil map) must propose no copies: a wrong
	// guess would plant files the user never asked for.
	if d := detectDefaults(root, nil); d.copies != nil {
		t.Errorf("copies with unknown tracking = %v, want none", d.copies)
	}
}

func TestDetectDefaultsSharedCacheNote(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "go.mod")
	d := detectDefaults(root, map[string]bool{})
	if d.refresh != "" {
		t.Errorf("refresh = %q, want none for a globally-cached ecosystem", d.refresh)
	}
	if len(d.infoNotes) != 1 {
		t.Errorf("infoNotes = %v, want the machine-wide-cache note", d.infoNotes)
	}
}

// An untracked lockfile never reaches a fresh tree, so gating on
// it would run the hook once and then never again.
func TestDetectDefaultsIgnoresUntrackedMarkers(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "Cargo.lock")
	if d := detectDefaults(root, map[string]bool{}); d.refresh != "" {
		t.Errorf("refresh = %q, want none for an untracked lockfile", d.refresh)
	}
}
