// Package nav resolves a user's query to the worktree they mean.
// It layers fuzzy matching (PLAN.md Phase 3) on top of the exact
// spellings the rest of wt accepts, with one deterministic rule:
// a query only ever jumps somewhere unambiguous.
package nav

import (
	"cmp"
	"path/filepath"
	"slices"

	"github.com/sahilm/fuzzy"

	"github.com/loganthomas/wt/internal/repo"
)

// maxContenders bounds the disambiguation list shown for an
// ambiguous query (PLAN.md Phase 3: top-5 on stderr).
const maxContenders = 5

// Candidate is one jump target: a worktree the user can land in.
type Candidate struct {
	Branch string // checked-out branch; empty when detached
	Path   string // absolute tree root
}

// Display names the candidate for human-facing lists.
func (c Candidate) Display() string {
	if c.Branch != "" {
		return c.Branch
	}
	return filepath.Base(c.Path) + " (detached)"
}

// names are the spellings a query is matched against:
// the branch, its sanitized directory form, and the directory
// basename — the same three the exact-name commands accept.
// Overlapping spellings are harmless: scoring takes the best
// name, so a duplicate can never change the result.
func (c Candidate) names() []string {
	names := []string{filepath.Base(c.Path)}
	if c.Branch != "" {
		names = append(names, c.Branch, repo.SanitizeBranch(c.Branch))
	}
	return names
}

// Resolve picks the candidate best matching query.
// Exact spellings win outright, branch names before directory
// names. Otherwise the best fuzzy score wins — but only when it
// strictly beats the runner-up: a tie never guesses, it returns
// the ranked contenders (at most five) instead.
// No match at all returns (nil, nil).
func Resolve(cands []Candidate, query string) (winner *Candidate, contenders []Candidate) {
	if query == "" {
		return nil, nil
	}
	for i, c := range cands {
		if c.Branch != "" && (c.Branch == query || repo.SanitizeBranch(c.Branch) == query) {
			return &cands[i], nil
		}
	}
	for i, c := range cands {
		if filepath.Base(c.Path) == query {
			return &cands[i], nil
		}
	}
	ranked := rank(cands, query)
	switch {
	case len(ranked) == 0:
		return nil, nil
	case len(ranked) == 1 || ranked[0].score > ranked[1].score:
		return &ranked[0].Candidate, nil
	default:
		for _, r := range ranked[:min(len(ranked), maxContenders)] {
			contenders = append(contenders, r.Candidate)
		}
		return nil, contenders
	}
}

type scored struct {
	Candidate
	score int
}

// rank orders the fuzzily matching candidates best-first,
// scoring each candidate by the best of its names.
// Ties keep input order, so the result is deterministic.
//
// Scores are match quality only: sahilm/fuzzy's −1-per-leftover-
// byte length penalty is normalized away by adding the byte
// length back. How well the query hits is what decides between
// trees, never how long the rest of a name happens to be —
// otherwise "feature" would "decisively" pick feature/login over
// feature/logout just because login is a letter shorter.
func rank(cands []Candidate, query string) []scored {
	var ranked []scored
	for _, c := range cands {
		best, matched := 0, false
		for _, m := range fuzzy.Find(query, c.names()) {
			if quality := m.Score + len(m.Str); !matched || quality > best {
				best, matched = quality, true
			}
		}
		if matched {
			ranked = append(ranked, scored{Candidate: c, score: best})
		}
	}
	slices.SortStableFunc(ranked, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
	return ranked
}
