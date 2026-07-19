package gitx

import (
	"fmt"
	"strings"
)

// Worktree is one record of `git worktree list --porcelain -z` output.
type Worktree struct {
	Path           string
	Head           string // full commit SHA; empty for a bare worktree
	Branch         string // short branch name; empty when detached or bare
	Bare           bool
	Detached       bool
	Locked         bool
	LockedReason   string
	Prunable       bool
	PrunableReason string
}

// ParseWorktrees parses `git worktree list --porcelain -z` output.
//
// The -z format NUL-terminates each attribute and separates records
// with an empty attribute; a missing final separator is tolerated.
// Unknown attributes are skipped so newer gits keep working.
func ParseWorktrees(out []byte) ([]Worktree, error) {
	var trees []Worktree
	var cur *Worktree
	for attr := range strings.SplitSeq(string(out), "\x00") {
		key, value, _ := strings.Cut(attr, " ")
		switch key {
		case "":
			if cur != nil {
				trees = append(trees, *cur)
				cur = nil
			}
		case "worktree":
			if cur != nil {
				trees = append(trees, *cur)
			}
			cur = &Worktree{Path: value}
		default:
			if cur == nil {
				return nil, fmt.Errorf("porcelain attribute %q before any worktree record", attr)
			}
			switch key {
			case "HEAD":
				cur.Head = value
			case "branch":
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare":
				cur.Bare = true
			case "detached":
				cur.Detached = true
			case "locked":
				cur.Locked = true
				cur.LockedReason = value
			case "prunable":
				cur.Prunable = true
				cur.PrunableReason = value
			}
		}
	}
	if cur != nil {
		trees = append(trees, *cur)
	}
	return trees, nil
}
