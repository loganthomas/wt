package pool_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/loganthomas/wt/internal/pool"
)

func TestSlotName(t *testing.T) {
	if got := pool.SlotName(3); got != "slot-3" {
		t.Errorf("SlotName(3) = %q, want slot-3", got)
	}
}

func TestNames(t *testing.T) {
	want := []string{"slot-1", "slot-2", "slot-3"}
	if got := pool.Names(3); !slices.Equal(got, want) {
		t.Errorf("Names(3) = %v, want %v", got, want)
	}
}

func TestIsSlotName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"slot-1", true},
		{"slot-12", true},
		// Only names wt itself would mint count as slots:
		// anything else must never be resettable (D14).
		{"slot-0", false},
		{"slot-03", false},
		{"slot-", false},
		{"slot-x", false},
		{"slot-1x", false},
		{"SLOT-1", false},
		{"my-slot-1", false},
		{"slot-1-backup", false},
		{"feature-login", false},
		{"", false},
	}
	for _, tt := range tests {
		if ok := pool.IsSlotName(tt.name); ok != tt.ok {
			t.Errorf("IsSlotName(%q) = %v, want %v", tt.name, ok, tt.ok)
		}
	}
}

func TestSlotIndex(t *testing.T) {
	tests := []struct {
		name string
		idx  int
		ok   bool
	}{
		{"slot-1", 1, true},
		{"slot-12", 12, true},
		{"slot-0", 0, false},
		{"slot-x", 0, false},
		{"feature-login", 0, false},
	}
	for _, tt := range tests {
		idx, ok := pool.SlotIndex(tt.name)
		if idx != tt.idx || ok != tt.ok {
			t.Errorf("SlotIndex(%q) = %d, %v; want %d, %v", tt.name, idx, ok, tt.idx, tt.ok)
		}
	}
}

// TestSlotPath drives the pattern guard with the hostile inputs
// from PLAN.md Phase 4: the main checkout, personal trees, and
// symlinks must all be structurally unresettable.
func TestSlotPath(t *testing.T) {
	root := t.TempDir()
	// Symlink-resolved so expectations match EvalSymlinks output
	// (macOS's temp dir is itself a symlink).
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "acme")
	trees := filepath.Join(root, "acme.trees")
	for _, dir := range []string{
		main,
		filepath.Join(trees, "slot-1"),
		filepath.Join(trees, "slot-1", "slot-2"), // nested decoy
		filepath.Join(trees, "feature-login"),    // personal tree
		filepath.Join(root, "elsewhere", "slot-1"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A symlink inside the container pointing at the main checkout:
	// the name matches, the target must not.
	if err := os.Symlink(main, filepath.Join(trees, "slot-9")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		slot string
		ok   bool
	}{
		{filepath.Join(trees, "slot-1"), "slot-1", true},
		{main, "", false},
		{trees, "", false},
		{filepath.Join(trees, "feature-login"), "", false},
		{filepath.Join(trees, "slot-1", "slot-2"), "", false},
		{filepath.Join(root, "elsewhere", "slot-1"), "", false},
		{filepath.Join(trees, "slot-9"), "", false}, // symlink to main
		{filepath.Join(trees, "no-such", "..", "slot-1"), "slot-1", true},
		{filepath.Join(trees, "does-not-exist"), "", false}, // fail closed
		{filepath.Join(trees, "slot-999"), "", false},       // fail closed: absent
	}
	for _, tt := range tests {
		slot, ok := pool.SlotPath(trees, tt.path)
		if slot != tt.slot || ok != tt.ok {
			t.Errorf("SlotPath(trees, %q) = %q, %v; want %q, %v",
				tt.path, slot, ok, tt.slot, tt.ok)
		}
	}

	// A symlinked container still recognizes its own slots:
	// git reports physical paths, config may hold the symlink.
	link := filepath.Join(root, "trees-link")
	if err := os.Symlink(trees, link); err != nil {
		t.Fatal(err)
	}
	if slot, ok := pool.SlotPath(link, filepath.Join(trees, "slot-1")); !ok || slot != "slot-1" {
		t.Errorf("SlotPath through symlinked container = %q, %v; want slot-1, true", slot, ok)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pnpm-lock.yaml", "lockfile-v1")
	writeFile(t, root, "other.lock", "other")

	base, err := pool.Hash(root, []string{"pnpm-lock.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if base == "" {
		t.Fatal("Hash returned empty")
	}

	same, err := pool.Hash(root, []string{"pnpm-lock.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if same != base {
		t.Error("Hash is not deterministic")
	}

	writeFile(t, root, "pnpm-lock.yaml", "lockfile-v2")
	changed, err := pool.Hash(root, []string{"pnpm-lock.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == base {
		t.Error("content change did not change the hash")
	}

	widened, err := pool.Hash(root, []string{"pnpm-lock.yaml", "other.lock"})
	if err != nil {
		t.Fatal(err)
	}
	if widened == changed {
		t.Error("adding a file to the list did not change the hash")
	}

	// A missing file and an empty file are different states:
	// deleting a lockfile must trigger a refresh.
	writeFile(t, root, "empty.lock", "")
	withEmpty, err := pool.Hash(root, []string{"empty.lock"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "empty.lock")); err != nil {
		t.Fatal(err)
	}
	withMissing, err := pool.Hash(root, []string{"empty.lock"})
	if err != nil {
		t.Fatal(err)
	}
	if withEmpty == withMissing {
		t.Error("missing file hashes like an empty file")
	}
}

// A case-insensitive filesystem (macOS by default) puts Slot-1
// exactly where slot-1 must go, so the reservation folds case
// where the destructive-path guard stays exact.
func TestCollidesWithSlotName(t *testing.T) {
	tests := []struct {
		name  string
		slot  bool
		clash bool
	}{
		{"slot-1", true, true},
		{"Slot-1", false, true},
		{"SLOT-12", false, true},
		{"slot-0", false, false},
		{"slot-1x", false, false},
		{"feature-login", false, false},
	}
	for _, tt := range tests {
		if got := pool.IsSlotName(tt.name); got != tt.slot {
			t.Errorf("IsSlotName(%q) = %v, want %v", tt.name, got, tt.slot)
		}
		if got := pool.CollidesWithSlotName(tt.name); got != tt.clash {
			t.Errorf("CollidesWithSlotName(%q) = %v, want %v", tt.name, got, tt.clash)
		}
	}
}
