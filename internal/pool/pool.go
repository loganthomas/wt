// Package pool holds the pure mechanics of pool mode (PLAN.md
// Phase 4): slot naming, the pattern guard that makes non-slot
// paths structurally unresettable (D14), and the refresh hash
// behind the lockfile gate (D5).
package pool

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// slotName is the one spelling of a slot: slot-N, N ≥ 1, no
// leading zeros: exactly the names wt itself mints, nothing more.
var slotName = regexp.MustCompile(`^slot-([1-9][0-9]*)$`)

// SlotName names the i-th pool slot (1-based).
func SlotName(i int) string {
	return fmt.Sprintf("slot-%d", i)
}

// Names lists every slot name of a pool of the given size.
func Names(size int) []string {
	names := make([]string, size)
	for i := range names {
		names[i] = SlotName(i + 1)
	}
	return names
}

// IsSlotName reports whether name is a slot wt itself would mint.
func IsSlotName(name string) bool {
	return slotName.MatchString(name)
}

// SlotIndex reports the 1-based index behind a slot name wt would
// mint, for callers that must compare a slot against a pool size.
func SlotIndex(name string) (int, bool) {
	m := slotName.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	i, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return i, true
}

// SlotPath reports whether path is a pool slot sitting directly
// inside treesDir, and which one. This is the pattern guard
// (D14): every reset, release, and slot removal must pass here
// first, so the main checkout and personal trees are structurally
// unresettable. Both sides are symlink-resolved before comparing
// (a symlink named like a slot but pointing elsewhere resolves
// elsewhere and is refused), and anything unresolvable fails
// closed.
func SlotPath(treesDir, path string) (string, bool) {
	container, err := filepath.EvalSymlinks(treesDir)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	if filepath.Dir(resolved) != container {
		return "", false
	}
	base := filepath.Base(resolved)
	if !IsSlotName(base) {
		return "", false
	}
	return base, true
}

// Hash fingerprints the named files under root for the refresh
// gate (D5): a claim re-runs hooks.refresh only when the hash
// differs from the recorded one. Name, presence, and content
// length are all bound into the digest, so an edit, an addition,
// a removal, and a rename each change the result, and no
// concatenation of inputs is ambiguous.
func Hash(root string, files []string) (string, error) {
	h := sha256.New()
	for _, name := range files {
		fmt.Fprintf(h, "%s\x00", name)
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprint(h, "absent\x00")
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:", len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
