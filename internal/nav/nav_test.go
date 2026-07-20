package nav

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func candidates() []Candidate {
	return []Candidate{
		{Branch: "main", Path: "/repos/acme"},
		{Branch: "feature/login", Path: "/repos/acme.trees/feature-login"},
		{Branch: "feature/logout", Path: "/repos/acme.trees/feature-logout"},
		{Branch: "", Path: "/repos/acme.trees/pool-3"},
	}
}

func TestResolveExactBranchWins(t *testing.T) {
	winner, contenders := Resolve(candidates(), "feature/login")
	if winner == nil || winner.Branch != "feature/login" {
		t.Fatalf("Resolve() winner = %v, contenders = %v; want feature/login", winner, contenders)
	}
}

func TestResolveExactSanitizedBranchWins(t *testing.T) {
	winner, _ := Resolve(candidates(), "feature-login")
	if winner == nil || winner.Branch != "feature/login" {
		t.Fatalf("Resolve() winner = %v; want feature/login", winner)
	}
}

func TestResolveExactBasenameWinsForDetachedTrees(t *testing.T) {
	winner, _ := Resolve(candidates(), "pool-3")
	if winner == nil || winner.Path != "/repos/acme.trees/pool-3" {
		t.Fatalf("Resolve() winner = %v; want the pool-3 tree", winner)
	}
}

// A branch-name match outranks another tree's directory name:
// when one tree's directory carries another tree's branch name,
// the user almost certainly means the branch.
func TestResolveExactBranchBeatsExactBasename(t *testing.T) {
	cands := []Candidate{
		{Branch: "fix", Path: "/trees/confusing"},
		{Branch: "other", Path: "/trees/fix"},
	}
	winner, _ := Resolve(cands, "fix")
	if winner == nil || winner.Branch != "fix" {
		t.Fatalf("Resolve() winner = %v; want the fix branch", winner)
	}
}

func TestResolveDecisiveFuzzyMatch(t *testing.T) {
	winner, contenders := Resolve(candidates(), "login")
	if winner == nil || winner.Branch != "feature/login" {
		t.Fatalf("Resolve() winner = %v, contenders = %v; want feature/login", winner, contenders)
	}
}

func TestResolveIsCaseInsensitive(t *testing.T) {
	winner, _ := Resolve(candidates(), "LOGIN")
	if winner == nil || winner.Branch != "feature/login" {
		t.Fatalf("Resolve() winner = %v; want feature/login", winner)
	}
}

func TestResolveMatchesDirectoryBasename(t *testing.T) {
	winner, _ := Resolve(candidates(), "pool")
	if winner == nil || winner.Path != "/repos/acme.trees/pool-3" {
		t.Fatalf("Resolve() winner = %v; want the pool-3 tree", winner)
	}
}

func TestResolveTieIsAmbiguous(t *testing.T) {
	cands := []Candidate{
		{Branch: "app-1", Path: "/trees/app-1"},
		{Branch: "app-2", Path: "/trees/app-2"},
	}
	winner, contenders := Resolve(cands, "app")
	if winner != nil {
		t.Fatalf("Resolve() winner = %v; want ambiguity", winner)
	}
	want := []Candidate{
		{Branch: "app-1", Path: "/trees/app-1"},
		{Branch: "app-2", Path: "/trees/app-2"},
	}
	if diff := cmp.Diff(want, contenders); diff != "" {
		t.Errorf("Resolve() contenders mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveAmbiguityCapsAtFiveContenders(t *testing.T) {
	var cands []Candidate
	for i := range 7 {
		name := fmt.Sprintf("app-%d", i)
		cands = append(cands, Candidate{Branch: name, Path: "/trees/" + name})
	}
	winner, contenders := Resolve(cands, "app")
	if winner != nil {
		t.Fatalf("Resolve() winner = %v; want ambiguity", winner)
	}
	if len(contenders) != 5 {
		t.Fatalf("Resolve() returned %d contenders, want 5", len(contenders))
	}
}

func TestResolveNoMatch(t *testing.T) {
	winner, contenders := Resolve(candidates(), "zzz")
	if winner != nil || contenders != nil {
		t.Fatalf("Resolve() = %v, %v; want no match", winner, contenders)
	}
}

func TestResolveEmptyQueryMatchesNothing(t *testing.T) {
	winner, contenders := Resolve(candidates(), "")
	if winner != nil || contenders != nil {
		t.Fatalf("Resolve() = %v, %v; want no match", winner, contenders)
	}
}

// One candidate matching through several of its names
// (branch and directory) must still count once.
func TestResolveDeduplicatesNamesPerCandidate(t *testing.T) {
	cands := []Candidate{
		{Branch: "feature/login", Path: "/trees/feature-login"},
		{Branch: "main", Path: "/trees/acme"},
	}
	winner, contenders := Resolve(cands, "login")
	if winner == nil {
		t.Fatalf("Resolve() ambiguous with contenders %v; want a decisive winner", contenders)
	}
}

func TestDisplayPrefersBranch(t *testing.T) {
	c := Candidate{Branch: "feature/login", Path: "/trees/feature-login"}
	if got := c.Display(); got != "feature/login" {
		t.Errorf("Display() = %q, want feature/login", got)
	}
}

func TestDisplayFallsBackToBasename(t *testing.T) {
	c := Candidate{Path: "/trees/pool-3"}
	if got := c.Display(); got != "pool-3 (detached)" {
		t.Errorf("Display() = %q, want 'pool-3 (detached)'", got)
	}
}
