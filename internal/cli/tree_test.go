package cli

import (
	"testing"

	"github.com/loganthomas/wt/internal/gitx"
)

func TestResolveTreeByName(t *testing.T) {
	// The directory named "api" carries branch "hotfix", while the
	// branch "api" lives in a differently-named directory:
	// the collision resolveTree must get right.
	trees := []gitx.Worktree{
		{Path: "/w/acme", Branch: "main"},
		{Path: "/w/acme.trees/api", Branch: "hotfix"},
		{Path: "/w/acme.trees/api-v2", Branch: "api"},
		{Path: "/w/acme.trees/feature-login", Branch: "feature/login"},
		{Path: "/w/acme.trees/scratch", Detached: true},
	}
	tests := []struct {
		name     string
		wantPath string
	}{
		{"api", "/w/acme.trees/api-v2"}, // branch match outranks the "api" directory
		{"hotfix", "/w/acme.trees/api"},
		{"feature/login", "/w/acme.trees/feature-login"},
		{"feature-login", "/w/acme.trees/feature-login"}, // sanitized spelling
		{"scratch", "/w/acme.trees/scratch"},             // detached: directory name only
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTree(t.Context(), trees, tt.name)
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != tt.wantPath {
				t.Errorf("resolveTree(%q).Path = %q, want %q", tt.name, got.Path, tt.wantPath)
			}
		})
	}

	t.Run("unknown name errors", func(t *testing.T) {
		if _, err := resolveTree(t.Context(), trees, "nope"); err == nil {
			t.Error("resolveTree(unknown) succeeded, want error")
		}
	})
}
