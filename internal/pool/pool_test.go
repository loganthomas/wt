package pool_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/loganthomas/wt/internal/pool"
)

func TestSlotName(t *testing.T) {
	if got := pool.SlotName(3); got != "pool-3" {
		t.Errorf("SlotName(3) = %q, want pool-3", got)
	}
}

func TestNames(t *testing.T) {
	want := []string{"pool-1", "pool-2", "pool-3"}
	if got := pool.Names(3); !slices.Equal(got, want) {
		t.Errorf("Names(3) = %v, want %v", got, want)
	}
}

func TestParseSlot(t *testing.T) {
	tests := []struct {
		name string
		n    int
		ok   bool
	}{
		{"pool-1", 1, true},
		{"pool-12", 12, true},
		// Only names wt itself would mint count as slots:
		// anything else must never be resettable (D14).
		{"pool-0", 0, false},
		{"pool-03", 0, false},
		{"pool-", 0, false},
		{"pool-x", 0, false},
		{"pool-1x", 0, false},
		{"POOL-1", 0, false},
		{"my-pool-1", 0, false},
		{"pool-1-backup", 0, false},
		{"feature-login", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		n, ok := pool.ParseSlot(tt.name)
		if n != tt.n || ok != tt.ok {
			t.Errorf("ParseSlot(%q) = %d, %v; want %d, %v", tt.name, n, ok, tt.n, tt.ok)
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
		filepath.Join(trees, "pool-1"),
		filepath.Join(trees, "pool-1", "pool-2"), // nested decoy
		filepath.Join(trees, "feature-login"),    // personal tree
		filepath.Join(root, "elsewhere", "pool-1"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A symlink inside the container pointing at the main checkout:
	// the name matches, the target must not.
	if err := os.Symlink(main, filepath.Join(trees, "pool-9")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		slot string
		ok   bool
	}{
		{filepath.Join(trees, "pool-1"), "pool-1", true},
		{main, "", false},
		{trees, "", false},
		{filepath.Join(trees, "feature-login"), "", false},
		{filepath.Join(trees, "pool-1", "pool-2"), "", false},
		{filepath.Join(root, "elsewhere", "pool-1"), "", false},
		{filepath.Join(trees, "pool-9"), "", false}, // symlink to main
		{filepath.Join(trees, "no-such", "..", "pool-1"), "pool-1", true},
		{filepath.Join(trees, "does-not-exist"), "", false}, // fail closed
		{filepath.Join(trees, "pool-999"), "", false},       // fail closed: absent
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
	if slot, ok := pool.SlotPath(link, filepath.Join(trees, "pool-1")); !ok || slot != "pool-1" {
		t.Errorf("SlotPath through symlinked container = %q, %v; want pool-1, true", slot, ok)
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
