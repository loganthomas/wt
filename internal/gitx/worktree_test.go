package gitx

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseWorktrees(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Worktree
	}{
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name: "single attached worktree",
			input: "worktree /repos/acme\x00" +
				"HEAD 1234567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00" +
				"\x00",
			want: []Worktree{{
				Path:   "/repos/acme",
				Head:   "1234567890abcdef1234567890abcdef12345678",
				Branch: "main",
			}},
		},
		{
			name: "linked worktree with slashed branch",
			input: "worktree /repos/acme\x00" +
				"HEAD aaaa567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00" +
				"\x00" +
				"worktree /repos/acme.trees/feature-login\x00" +
				"HEAD bbbb567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/feature/login\x00" +
				"\x00",
			want: []Worktree{
				{
					Path:   "/repos/acme",
					Head:   "aaaa567890abcdef1234567890abcdef12345678",
					Branch: "main",
				},
				{
					Path:   "/repos/acme.trees/feature-login",
					Head:   "bbbb567890abcdef1234567890abcdef12345678",
					Branch: "feature/login",
				},
			},
		},
		{
			name: "detached head",
			input: "worktree /repos/acme.trees/pool-1\x00" +
				"HEAD cccc567890abcdef1234567890abcdef12345678\x00" +
				"detached\x00" +
				"\x00",
			want: []Worktree{{
				Path:     "/repos/acme.trees/pool-1",
				Head:     "cccc567890abcdef1234567890abcdef12345678",
				Detached: true,
			}},
		},
		{
			name:  "bare repository",
			input: "worktree /repos/acme.git\x00bare\x00\x00",
			want: []Worktree{{
				Path: "/repos/acme.git",
				Bare: true,
			}},
		},
		{
			name: "locked without reason",
			input: "worktree /Volumes/usb/tree\x00" +
				"HEAD dddd567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00" +
				"locked\x00" +
				"\x00",
			want: []Worktree{{
				Path:   "/Volumes/usb/tree",
				Head:   "dddd567890abcdef1234567890abcdef12345678",
				Branch: "main",
				Locked: true,
			}},
		},
		{
			name: "locked with reason",
			input: "worktree /Volumes/usb/tree\x00" +
				"HEAD dddd567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00" +
				"locked usb drive unplugged\x00" +
				"\x00",
			want: []Worktree{{
				Path:         "/Volumes/usb/tree",
				Head:         "dddd567890abcdef1234567890abcdef12345678",
				Branch:       "main",
				Locked:       true,
				LockedReason: "usb drive unplugged",
			}},
		},
		{
			name: "prunable with reason",
			input: "worktree /repos/gone\x00" +
				"HEAD eeee567890abcdef1234567890abcdef12345678\x00" +
				"detached\x00" +
				"prunable gitdir file points to non-existent location\x00" +
				"\x00",
			want: []Worktree{{
				Path:           "/repos/gone",
				Head:           "eeee567890abcdef1234567890abcdef12345678",
				Detached:       true,
				Prunable:       true,
				PrunableReason: "gitdir file points to non-existent location",
			}},
		},
		{
			name: "unknown attribute is ignored for forward compatibility",
			input: "worktree /repos/acme\x00" +
				"HEAD ffff567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00" +
				"shinynewthing value\x00" +
				"\x00",
			want: []Worktree{{
				Path:   "/repos/acme",
				Head:   "ffff567890abcdef1234567890abcdef12345678",
				Branch: "main",
			}},
		},
		{
			name: "missing separator between records is tolerated",
			input: "worktree /repos/a\x00" +
				"HEAD 1111567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00" +
				"worktree /repos/b\x00" +
				"bare\x00",
			want: []Worktree{
				{
					Path:   "/repos/a",
					Head:   "1111567890abcdef1234567890abcdef12345678",
					Branch: "main",
				},
				{
					Path: "/repos/b",
					Bare: true,
				},
			},
		},
		{
			name: "missing trailing record separator is tolerated",
			input: "worktree /repos/acme\x00" +
				"HEAD 1234567890abcdef1234567890abcdef12345678\x00" +
				"branch refs/heads/main\x00",
			want: []Worktree{{
				Path:   "/repos/acme",
				Head:   "1234567890abcdef1234567890abcdef12345678",
				Branch: "main",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseWorktrees([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseWorktrees() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseWorktrees() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseWorktreesRejectsAttributeBeforeRecord(t *testing.T) {
	_, err := ParseWorktrees([]byte("HEAD 1234\x00worktree /repos/acme\x00\x00"))
	if err == nil {
		t.Fatal("ParseWorktrees() expected error for attribute before worktree record, got nil")
	}
}
