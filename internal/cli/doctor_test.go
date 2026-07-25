package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loganthomas/wt/internal/gitx"
)

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		v    string
		want bool
	}{
		{"2.38.0", true},
		{"2.50.1", true},
		{"3.0", true},
		{"2.38", true},
		{"2.37.9", false},
		{"1.99.9", false},
		{"nonsense", false},
		{"2", false},
	}
	for _, tt := range tests {
		if got := versionAtLeast(tt.v, 2, 38); got != tt.want {
			t.Errorf("versionAtLeast(%q, 2, 38) = %v, want %v", tt.v, got, tt.want)
		}
	}
}

func TestCheckShim(t *testing.T) {
	tests := []struct {
		name, sig  string
		wantStatus string
		wantIn     string
	}{
		{"absent", "", "warn", "not active"},
		{"stale", "deadbeefdead", "warn", "different wt builds"},
		{"current", shimSig(), "ok", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkShim(tt.sig)
			if got.Status != tt.wantStatus {
				t.Errorf("checkShim(%q).Status = %q, want %q", tt.sig, got.Status, tt.wantStatus)
			}
			if tt.wantIn != "" && !strings.Contains(got.Symptom, tt.wantIn) {
				t.Errorf("checkShim(%q).Symptom = %q, want it to contain %q",
					tt.sig, got.Symptom, tt.wantIn)
			}
		})
	}
}

func TestCheckBranchDuplicates(t *testing.T) {
	clean := []gitx.Worktree{
		{Branch: "main", Path: "/repo"},
		{Branch: "feat", Path: "/trees/feat"},
		{Detached: true, Path: "/trees/scratch"},
		{Detached: true, Path: "/trees/scratch2"},
	}
	if got := checkBranchDuplicates(clean); got.Status != "ok" {
		t.Errorf("checkBranchDuplicates(clean) = %q, want ok", got.Status)
	}
	dup := append(clean, gitx.Worktree{Branch: "feat", Path: "/elsewhere/feat"},
		gitx.Worktree{Branch: "main", Path: "/elsewhere/main"})
	got := checkBranchDuplicates(dup)
	if got.Status != "fail" {
		t.Errorf("checkBranchDuplicates(dup).Status = %q, want fail", got.Status)
	}
	// Every duplicated branch, in sorted order, so the symptom is
	// deterministic for machine consumers.
	if !strings.Contains(got.Symptom, "feat (2 trees), main (2 trees)") {
		t.Errorf("checkBranchDuplicates(dup).Symptom = %q, want both branches sorted",
			got.Symptom)
	}
}

func TestCheckHooksPath(t *testing.T) {
	if got := checkHooksPath("", nil); got.Status != "ok" {
		t.Errorf("checkHooksPath(unset) = %q, want ok", got.Status)
	}
	got := checkHooksPath(".husky", nil)
	if got.Status != "warn" || !strings.Contains(got.Symptom, ".husky") {
		t.Errorf("checkHooksPath(.husky) = %q %q, want a warn naming the path",
			got.Status, got.Symptom)
	}
}

func TestParseReleaseTag(t *testing.T) {
	r, ok := parseRelease("v1.2.3")
	if !ok || r.major != 1 || r.minor != 2 || r.patch != 3 || r.pre {
		t.Errorf("parseRelease(v1.2.3) = %+v, %v", r, ok)
	}
	r, ok = parseRelease("0.1.0-alpha.6")
	if !ok || !r.pre {
		t.Errorf("parseRelease(0.1.0-alpha.6) = %+v, %v; want a prerelease", r, ok)
	}
	if _, ok := parseRelease("garbage"); ok {
		t.Error("parseRelease(garbage) = ok, want a refusal")
	}
}

func TestNewerRelease(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},
		{"v0.1.0", "0.2.0", false},
		{"v0.1.1", "0.1.0", true},
		// A release outranks the same version's prereleases.
		{"v0.1.0", "0.1.0-alpha.6", true},
	}
	for _, tt := range tests {
		latest, ok1 := parseRelease(tt.latest)
		current, ok2 := parseRelease(tt.current)
		if !ok1 || !ok2 {
			t.Fatalf("fixture tags did not parse: %q %q", tt.latest, tt.current)
		}
		if got := newerRelease(latest, current); got != tt.want {
			t.Errorf("newerRelease(%s, %s) = %v, want %v",
				tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "v0.2.0"}`)) //nolint:errcheck // test stub
	}))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = old }()

	got := checkUpdate(t.Context(), "0.1.0", false)
	if got.Status != "info" || !strings.Contains(got.Symptom, "v0.2.0") {
		t.Errorf("checkUpdate(behind) = %q %q, want info naming v0.2.0",
			got.Status, got.Symptom)
	}
	if got.Fix == "" {
		t.Error("checkUpdate(behind).Fix is empty, want the upgrade command")
	}
	got = checkUpdate(t.Context(), "0.2.0", false)
	if got.Status != "ok" {
		t.Errorf("checkUpdate(current) = %q, want ok", got.Status)
	}
	got = checkUpdate(t.Context(), "0.1.0", true)
	if got.Status != "info" || !strings.Contains(got.Symptom, "skipped") {
		t.Errorf("checkUpdate(offline) = %q %q, want the skip note", got.Status, got.Symptom)
	}
	got = checkUpdate(t.Context(), "", false)
	if got.Status != "info" || !strings.Contains(got.Symptom, "dev build") {
		t.Errorf("checkUpdate(dev) = %q %q, want the dev-build note", got.Status, got.Symptom)
	}
}

// Until the first stable release ships, /releases/latest 404s for
// every alpha install; that is a fact to report, not a failure.
func TestCheckUpdateReportsNoStableRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = old }()

	got := checkUpdate(t.Context(), "0.1.0-alpha.6", false)
	if got.Status != "info" || !strings.Contains(got.Symptom, "no stable release") {
		t.Errorf("checkUpdate(404) = %q %q, want the no-stable-release note",
			got.Status, got.Symptom)
	}
}

func TestCheckUpdateSurvivesAPIFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = old }()

	got := checkUpdate(t.Context(), "0.1.0", false)
	if got.Status != "info" {
		t.Errorf("checkUpdate(API down) = %q, want info, never a failure", got.Status)
	}
}

func TestFormatDoctorIndentsCauseAndFix(t *testing.T) {
	view := doctorView{
		Checks: []checkResult{
			{Name: "git", Status: "ok", Symptom: "2.50.1"},
			{
				Name: "config", Status: "fail",
				Symptom: "wt.toml:3:1: unknown key",
				Cause:   "the file was edited by hand",
				Fix:     "wt config --edit",
			},
		},
		Issues: 1,
	}
	got := formatDoctor(view)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "ok") || !strings.Contains(lines[0], "2.50.1") {
		t.Errorf("first row = %q, want the ok git row", lines[0])
	}
	var cause, fix bool
	for _, l := range lines {
		if strings.HasPrefix(l, " ") && strings.Contains(l, "cause:") {
			cause = true
		}
		if strings.HasPrefix(l, " ") && strings.Contains(l, "fix:") {
			fix = true
		}
	}
	if !cause || !fix {
		t.Errorf("formatDoctor() missing indented cause/fix lines:\n%s", got)
	}
}
