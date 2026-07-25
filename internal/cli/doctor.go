// wt doctor (PLAN.md Phase 6): actionable diagnostics. Every
// check reports symptom → cause → exact fix; a fail is a real
// issue and exits 3, a warn or info is advisory and never fails
// a script. Doctor also runs outside a repository — it is the
// support command, so it must never refuse to diagnose — and its
// only network call, the release check, is skippable and
// best-effort (D8).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/loganthomas/wt/internal/gitx"
	"github.com/loganthomas/wt/internal/lease"
	"github.com/loganthomas/wt/internal/render"
	"github.com/loganthomas/wt/internal/repo"
	"github.com/loganthomas/wt/internal/state"
)

// gitFloor is the oldest git wt supports: worktree behaviors wt
// relies on stabilized in 2.38.
const gitFloorMajor, gitFloorMinor = 2, 38

var (
	// Overridable seams for the release check: the URL by unit
	// tests, the client timeout so a slow API cannot hang doctor.
	releasesURL    = "https://api.github.com/repos/loganthomas/wt/releases/latest"
	releasesClient = &http.Client{Timeout: 3 * time.Second}
)

func newDoctorCmd(info BuildInfo) *cobra.Command {
	var jsonOut, offline bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the setup: each symptom with cause and exact fix",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, info, jsonOut, offline)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the release update check")
	return cmd
}

// checkResult is one diagnostic's outcome. Status is the machine
// vocabulary: ok, info, warn, fail — only fail counts as an issue.
type checkResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Symptom string `json:"symptom,omitempty"`
	Cause   string `json:"cause,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

type doctorView struct {
	Checks []checkResult `json:"checks"`
	Issues int           `json:"issues"`
}

func runDoctor(cmd *cobra.Command, info BuildInfo, jsonOut, offline bool) error {
	ctx := cmd.Context()
	var view doctorView
	add := func(r checkResult) {
		view.Checks = append(view.Checks, r)
		if r.Status == "fail" {
			view.Issues++
		}
	}

	v, verr := gitx.New("").Version(ctx)
	add(checkGitVersion(v, verr))
	add(checkShim(os.Getenv(shimSigEnv)))
	repoChecks(ctx, add)
	add(checkUpdate(ctx, info.Version, offline))

	out := cmd.OutOrStdout()
	if jsonOut {
		if err := render.JSON(out, view); err != nil {
			return err
		}
	} else if _, err := fmt.Fprint(out, formatDoctor(view)); err != nil {
		return err
	}
	if view.Issues > 0 {
		return preconditionf("%d %s found — the fixes are listed above",
			view.Issues, plural(view.Issues, "issue"))
	}
	return nil
}

// repoChecks runs the diagnostics that only mean something inside
// a repository; outside one they are simply absent, not failures.
// Everything that breaks in here becomes a finding, never an
// abort: doctor renders whatever it managed to learn — a corrupt
// repository or an unrunnable git is precisely when the report is
// needed most. A broken config is likewise itself a finding, so
// the checks that need config values are skipped, not aborted.
func repoChecks(ctx context.Context, add func(checkResult)) {
	r, err := repo.Find(ctx, "")
	var notRepo *repo.NotARepoError
	if errors.As(err, &notRepo) {
		return
	}
	if err != nil {
		add(checkResult{
			Name: "repo", Status: "fail",
			Symptom: err.Error(),
			Cause:   "wt cannot resolve the repository around this directory",
			Fix:     "`git status` shows git's own view of it",
		})
		return
	}
	g := gitx.New(r.Root)
	trees, err := g.Worktrees(ctx)
	if err != nil {
		add(checkResult{
			Name: "worktrees", Status: "fail",
			Symptom: err.Error(),
			Cause:   "git could not list this repository's worktrees",
			Fix:     "git worktree list",
		})
		return
	}
	cfg, cfgErr := loadMerged(r)
	add(checkConfig(cfgErr))
	add(checkWorktrees(trees))
	add(checkBranchDuplicates(trees))
	add(checkSubmodules(r.Root))
	hooksPath, hooksErr := g.ConfigGet(ctx, "core.hooksPath")
	add(checkHooksPath(hooksPath, hooksErr))
	if cfgErr != nil {
		return
	}
	add(checkTreesVolume(r.Root, r.TreesDir(cfg.TreesDir)))
	if cfg.Pool == nil {
		return
	}
	sd, err := r.StateDir()
	if err != nil {
		add(checkResult{
			Name: "leases", Status: "info",
			Symptom: fmt.Sprintf("state directory unavailable (%v)", err),
		})
		return
	}
	add(checkLeases(state.Dir(sd)))
}

func checkGitVersion(v string, err error) checkResult {
	c := checkResult{Name: "git"}
	switch {
	case err != nil:
		c.Status = "fail"
		c.Symptom = fmt.Sprintf("git did not run (%v)", err)
		c.Cause = "wt shells out to the real git for every operation"
		c.Fix = "install git ≥ 2.38 — `brew install git`"
	case !versionAtLeast(v, gitFloorMajor, gitFloorMinor):
		c.Status = "fail"
		c.Symptom = fmt.Sprintf("git %s predates %d.%d", v, gitFloorMajor, gitFloorMinor)
		c.Cause = "the worktree behaviors wt relies on stabilized there"
		c.Fix = "`brew install git`, or upgrade via your package manager"
	default:
		c.Status, c.Symptom = "ok", v
	}
	return c
}

// versionAtLeast compares a dotted version's leading major.minor
// against a floor; anything unparseable fails the comparison and
// surfaces as the caller's symptom.
func versionAtLeast(v string, wantMajor, wantMinor int) bool {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}

func checkShim(sig string) checkResult {
	c := checkResult{Name: "shell-shim"}
	switch sig {
	case shimSig():
		c.Status, c.Symptom = "ok", "active and current"
	case "":
		c.Status = "warn"
		c.Symptom = "not active in this shell"
		c.Cause = "the eval line is missing from ~/.zshrc, or this is a non-interactive shell"
		c.Fix = `add eval "$(wt shell-init zsh)" to ~/.zshrc, then restart the shell`
	default:
		c.Status = "warn"
		c.Symptom = "emitted by an older wt"
		c.Cause = "this shell started before wt was upgraded"
		c.Fix = "restart the shell — `exec zsh`"
	}
	return c
}

func checkConfig(err error) checkResult {
	if err == nil {
		return checkResult{Name: "config", Status: "ok", Symptom: "parses cleanly"}
	}
	return checkResult{
		Name: "config", Status: "fail",
		Symptom: err.Error(),
		Cause:   "wt refuses to run on half-read settings",
		Fix:     "wt config --edit",
	}
}

func checkWorktrees(trees []gitx.Worktree) checkResult {
	c := checkResult{Name: "worktrees"}
	prunable, locked := 0, 0
	for _, t := range trees {
		if t.Prunable {
			prunable++
		}
		if t.Locked {
			locked++
		}
	}
	switch {
	case prunable > 0:
		c.Status = "fail"
		c.Symptom = fmt.Sprintf("%d registered %s gone from disk",
			prunable, plural(prunable, "tree"))
		c.Cause = "a tree directory was deleted without telling git"
		c.Fix = "wt clean"
	case locked > 0:
		c.Status = "warn"
		c.Symptom = fmt.Sprintf("%d locked %s", locked, plural(locked, "tree"))
		c.Cause = "locks are honored: wt done and wt clean refuse locked trees"
		c.Fix = "`git worktree unlock <path>` when the lock has served its purpose"
	default:
		c.Status = "ok"
		c.Symptom = fmt.Sprintf("%d %s, none locked or prunable",
			len(trees), plural(len(trees), "tree"))
	}
	return c
}

func checkBranchDuplicates(trees []gitx.Worktree) checkResult {
	c := checkResult{Name: "branches"}
	seen := make(map[string]int, len(trees))
	for _, t := range trees {
		if t.Branch != "" {
			seen[t.Branch]++
		}
	}
	for branch, n := range seen {
		if n > 1 {
			c.Status = "fail"
			c.Symptom = fmt.Sprintf("branch %s is checked out in %d trees", branch, n)
			c.Cause = "two trees on one branch silently diverge each other's HEAD"
			c.Fix = "`git worktree list` shows both — remove one of them"
			return c
		}
	}
	c.Status, c.Symptom = "ok", "no branch is checked out twice"
	return c
}

func checkSubmodules(root string) checkResult {
	c := checkResult{Name: "submodules"}
	if _, err := os.Stat(filepath.Join(root, ".gitmodules")); err != nil {
		c.Status, c.Symptom = "ok", "none"
		return c
	}
	c.Status = "warn"
	c.Symptom = "submodules present"
	c.Cause = "git supports worktrees with submodules, but wt adds no smoothing (R5)"
	c.Fix = "run `git submodule update --init` inside new trees; see docs/faq.md"
	return c
}

func checkHooksPath(path string, err error) checkResult {
	c := checkResult{Name: "hooks-path"}
	switch {
	case err != nil:
		c.Status = "info"
		c.Symptom = fmt.Sprintf("could not read core.hooksPath (%v)", err)
	case path == "":
		c.Status, c.Symptom = "ok", "default hooks path"
	case filepath.IsAbs(path):
		c.Status, c.Symptom = "ok", fmt.Sprintf("core.hooksPath = %s (absolute, shared)", path)
	default:
		c.Status = "warn"
		c.Symptom = fmt.Sprintf("core.hooksPath = %s (relative)", path)
		c.Cause = "a relative hooks path resolves inside each tree; " +
			"hooks vanish in trees where it is untracked (R7)"
		c.Fix = "`git config core.hooksPath <absolute path>`, " +
			"or keep the hooks directory tracked"
	}
	return c
}

// checkTreesVolume warns when the trees container lives on a
// different filesystem from the repo (D14: siblings keep trees on
// one volume, where git's object sharing stays cheap).
func checkTreesVolume(root, treesDir string) checkResult {
	c := checkResult{Name: "trees-volume"}
	rootDev, ok := deviceOf(root)
	if !ok {
		c.Status, c.Symptom = "info", "could not stat the repo root"
		return c
	}
	probe := treesDir
	treesDev, ok := deviceOf(probe)
	if !ok {
		// Not created yet: judge by where it would be created.
		probe = filepath.Dir(treesDir)
		if treesDev, ok = deviceOf(probe); !ok {
			c.Status = "info"
			c.Symptom = fmt.Sprintf("trees dir %s does not exist yet", treesDir)
			return c
		}
	}
	if rootDev != treesDev {
		c.Status = "warn"
		c.Symptom = fmt.Sprintf("trees dir %s sits on a different volume", treesDir)
		c.Cause = "cross-volume trees pay full copies where one volume shares cheaply"
		c.Fix = "set trees_dir in wt.toml to a path on the repo's volume"
		return c
	}
	c.Status, c.Symptom = "ok", "trees share the repo's volume"
	return c
}

func deviceOf(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev's width differs per platform
}

func checkLeases(st state.Dir) checkResult {
	c := checkResult{Name: "leases"}
	slots, err := lease.Slots(st.LeasesDir())
	if err != nil {
		c.Status, c.Symptom = "info", fmt.Sprintf("could not list leases (%v)", err)
		return c
	}
	var stale, unreadable []string
	for _, slot := range slots {
		held, err := lease.Get(st.LeasesDir(), slot)
		switch {
		case err != nil:
			unreadable = append(unreadable, slot)
		case held != nil && held.Stale():
			stale = append(stale, slot)
		}
	}
	switch {
	case len(unreadable) > 0:
		c.Status = "fail"
		c.Symptom = fmt.Sprintf("%s lease record unreadable", strings.Join(unreadable, ", "))
		c.Cause = "a torn write or filesystem fault; wt never steals what it cannot prove dead"
		c.Fix = fmt.Sprintf("wt release %s", unreadable[0])
	case len(stale) > 0:
		c.Status = "fail"
		c.Symptom = fmt.Sprintf("%d dead %s (%s)",
			len(stale), plural(len(stale), "lease"), strings.Join(stale, ", "))
		c.Cause = "a session died holding its slot"
		c.Fix = "wt clean"
	default:
		c.Status, c.Symptom = "ok", "no dead leases"
	}
	return c
}

// checkUpdate asks the GitHub releases API for the latest release
// (which by definition excludes prereleases) and compares. Only
// doctor makes this call — an explicit command, so the network
// access is consented (D8) — and every failure is informational:
// being offline is not a health problem.
func checkUpdate(ctx context.Context, version string, offline bool) checkResult {
	c := checkResult{Name: "update"}
	current, ok := parseRelease(version)
	switch {
	case offline:
		c.Status, c.Symptom = "info", "check skipped (--offline)"
		return c
	case version == "":
		c.Status, c.Symptom = "info", "dev build — check skipped"
		return c
	case !ok:
		c.Status, c.Symptom = "info", fmt.Sprintf("unrecognized version %q — check skipped", version)
		return c
	}
	latest, err := fetchLatestRelease(ctx)
	if errors.Is(err, errNoStableRelease) {
		c.Status = "info"
		c.Symptom = fmt.Sprintf("no stable release published yet (running %s)", version)
		return c
	}
	if err != nil {
		c.Status, c.Symptom = "info", fmt.Sprintf("check failed (%v)", err)
		return c
	}
	tag, ok := parseRelease(latest)
	if !ok {
		c.Status, c.Symptom = "info", fmt.Sprintf("unrecognized release tag %q", latest)
		return c
	}
	if newerRelease(tag, current) {
		c.Status = "info"
		c.Symptom = fmt.Sprintf("%s is available (running %s)", latest, version)
		c.Fix = "brew upgrade wt"
		return c
	}
	c.Status, c.Symptom = "ok", fmt.Sprintf("up to date (%s)", version)
	return c
}

// errNoStableRelease is the releases API's 404: the endpoint
// excludes prereleases, so a project that has only ever tagged
// alphas has no "latest release" — a fact, not a failure.
var errNoStableRelease = errors.New("no stable release published")

func fetchLatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := releasesClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode == http.StatusNotFound {
		return "", errNoStableRelease
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from the releases API", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// release is a parsed semver tag, prerelease reduced to a flag:
// wt only ever needs "is that release newer than what runs here".
type release struct {
	major, minor, patch int
	pre                 bool
}

func parseRelease(tag string) (release, bool) {
	tag = strings.TrimPrefix(tag, "v")
	base, rest, hasPre := strings.Cut(tag, "-")
	parts := strings.Split(base, ".")
	if len(parts) != 3 || (hasPre && rest == "") {
		return release{}, false
	}
	var r release
	for i, dst := range []*int{&r.major, &r.minor, &r.patch} {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return release{}, false
		}
		*dst = n
	}
	r.pre = hasPre
	return r, true
}

// newerRelease reports whether latest outranks current. On equal
// numbers a full release outranks a prerelease of itself, which
// is exactly the alpha-to-release upgrade moment.
func newerRelease(latest, current release) bool {
	l := [3]int{latest.major, latest.minor, latest.patch}
	c := [3]int{current.major, current.minor, current.patch}
	if l != c {
		return l[0] > c[0] || (l[0] == c[0] && (l[1] > c[1] || (l[1] == c[1] && l[2] > c[2])))
	}
	return current.pre && !latest.pre
}

// formatDoctor lays the checks out for humans: one aligned row
// per check, causes and fixes indented under their symptom. The
// continuation rows ride through render.Align as rows with empty
// leading cells, so the indent always lands at the symptom column.
func formatDoctor(view doctorView) string {
	var rows [][]string
	for _, c := range view.Checks {
		rows = append(rows, []string{c.Status, c.Name, c.Symptom})
		if c.Cause != "" {
			rows = append(rows, []string{"", "", "cause: " + c.Cause})
		}
		if c.Fix != "" {
			rows = append(rows, []string{"", "", "fix: " + c.Fix})
		}
	}
	return render.Align(rows)
}
