# `wt` — Design & Implementation Plan

A thin, elegant Go wrapper around `git worktree`.
Two behaviors from one binary:

- **Default mode** — ordinary repos: create / list / jump-to / remove worktrees
  with sane paths, fuzzy navigation, and safe cleanup.
- **Pool mode** (opt-in, per repo) — monorepos where cold worktrees are unusable
  (10-minute installs, 750k-file `node_modules`):
  a fixed pool of pre-warmed, reusable slots that are claimed, worked in, and released,
  with lockfile-hash short-circuiting so a claim takes seconds, not minutes.

Non-negotiable qualities:
trivial install/uninstall; works out of the box; fast
(startup dominated by git subprocesses, never by `wt` itself);
beautiful but restrained CLI;
machine-readable output as a first-class contract
(paths on stdout, chatter on stderr, `--json` where useful)
so agents work naturally without being the headline feature;
TDD throughout.

**v1 scope: macOS + zsh only**, stated plainly in the README.
The design doesn't preclude Linux/bash/fish; no v1 work goes there.

**Provenance:** informed by examples using Bash 3.2 pool manager
(atomic mkdir leases, warm-cache resets, safety guards — kept;
wall-clock reaping, no tests, no liveness — fixed)
and the daveschumaker.net worktree post (https://daveschumaker.net/use-git-worktrees-they-said-itll-be-fun-they-said/)
(pre-warmed pool + "reinstall only if lockfile changed" is the winning monorepo pattern).
This is a greenfield design, not a port.

---

## Decision Record

Format: **Decision / Alternatives / Why**.
Each was contested by an internal panel
(performance zealot, keep-it-boring pragmatist, DX advocate, long-term maintainer);
verdicts noted where the debate was real.

### D1. Language: Go

**Decision:** Go. Single static binary + a thin zsh shim.

**Alternatives:** Rust, Bash, Python, Zig.

**Why — including "is it even close to bash?":**
Bash has exactly one structural advantage: it runs _in_ the shell,
so `cd` and prompt changes are free.
Every respected tool in this space
(zoxide, direnv, starship, fnm, atuin, git-town)
abandoned that advantage for a compiled binary plus a ~20-line eval shim,
because the binary wins everything else:

- **Testability** — the TDD requirement is nearly unmeetable in bash;
  the reference tool's 1,100 lines have zero tests, and that's typical.
  Go gets table-driven units plus `testscript` end-to-end runs.
- **macOS ships bash 3.2** — the exact constraint that contorted the reference
  tool into disk-backed fake data structures.
- **Startup is a wash** — bash ~5–10ms, Go ~3–8ms;
  both are noise next to the git subprocesses that dominate every operation.
  Rust's ~2–5ms buys nothing measurable here.
- **Distribution** — one file to install and uninstall; embedded fuzzy matching
  removes the fzf dependency bash tools require.

Rust is equally fast but a language Logan would be learning while maintaining.
Python's interpreter startup fails the prompt-hook latency bar
and its packaging fails "trivial install."
Zig's CLI ecosystem is immature.
_Verdict: the performance zealot wanted Rust; overruled —
the hot path is `git fetch`, not the binary._

### D2. Git backend: shell out to the `git` binary

**Decision:** exec `git` via one internal `gitx` package; never link a git library.

**Alternatives:** go-git (worktree support is experimental and checkout is
markedly slower than C git on large repos); libgit2/git2go (cgo kills the
static-binary story).

**Why:** the git CLI _is_ the compatibility target;
`git worktree list --porcelain -z` is a stable parse surface;
users' config, credentials, and hooks behave exactly as they expect.

### D3. Mode naming: kill "mode" as a top-level concept

**Decision:** no user-facing `mode =` setting.
A repo either has a `[pool]` section in its config or it doesn't.
Docs say **default mode** and **pool mode**;
"standard"/"recycle" are dropped.

**Alternatives:** explicit mode enum; the name "recycle."

**Why:** "recycle" evokes garbage;
"pool" is a precise systems term (worker pool, connection pool)
and yields a natural verb namespace (`wt pool resize`).
Implicit mode removes a concept users must learn —
`wt init` asks one question
("big repo that needs pre-warmed worktrees?") and that's it.
_Verdict: DX advocate wanted an explicit switch for discoverability; overruled._

### D4. Config: `.git/wt.toml` per repo + XDG global defaults, TOML

**Decision:**

- Per-repo config: **`<git-common-dir>/wt.toml`**
  (i.e. `.git/wt.toml` in the main checkout), written by `wt init`.
  Resolved via `git rev-parse --git-common-dir`
  so every linked worktree sees the same config.
  Discoverability mitigated by `wt config [--edit]`
  (prints the path / opens it in `$VISUAL`/`$EDITOR`).
- Global defaults: `~/.config/wt/config.toml` (XDG).
- State (leases, timestamps, hashes):
  `~/.local/state/wt/repos/<slug>-<hash8>/`
  (slug = repo basename, hash8 = SHA-256 prefix of the common git dir path —
  readable _and_ collision-proof).
- Format: TOML via `github.com/pelletier/go-toml/v2`.

**Alternatives:**
repo-root `wt.toml` + global gitignore;
`~/.config/wt/repos/<hash>.toml` (invisible from the repo, orphans silently);
a committed `.wt.toml` (pool size and machine paths are per-person/per-machine —
the same user runs pool mode at work and default mode at home on one machine);
YAML (footguns); JSON (no comments).

**Why not root `wt.toml` + global ignore** (the natural-looking alternative):

1. **Untracked files don't propagate to linked worktrees.**
   Git only checks out tracked files,
   so a root `wt.toml` written in the main checkout
   simply doesn't exist in any other worktree —
   wt would have to special-case resolution back to the main checkout root anyway.
   The `.git` common dir is shared by every worktree for free;
   that's the property we actually need.
2. **`git clean -x` deletes ignored files.**
   A gitignored root config is one `git clean -fdx` away from destruction —
   in a tool whose pool reset is built on `git clean`, that's a footgun.
   Nothing under `.git/` is ever touched by clean.
3. **A global ignore is per-user machine setup.**
   Every collaborator must configure `core.excludesFile` or they see
   untracked noise in `git status` and risk committing the file.
   The pre-commit analogy actually cuts the other way:
   `.pre-commit-config.yaml` is _committed and shared_ project config;
   wt's per-repo config is per-person, per-machine —
   the kind git itself keeps inside `.git`
   (`.git/config`, `.git/info/exclude` are the precedent).

TOML is the Go-CLI norm, comment-friendly, and boring.
Post-1.0 option: a _committed_ `.wt.toml` of project-suggested defaults
that `wt init` reads as hints (that one, like pre-commit's file,
would be shared and version-controlled) — deferred.

### D5. Resource sharing across trees: hooks + hash gate; docs for the rest

**Decision:** `wt` stays ecosystem-agnostic and provides:

1. `hooks.setup` — run once when a tree is created or a slot is provisioned.
2. `hooks.refresh` — run on every claim/new,
   **but only if** the SHA-256 of the files in `hooks.refresh_if_changed`
   (e.g. `pnpm-lock.yaml`) differs from the hash recorded for that tree.
   This is the blog post's winning pattern, productized.
3. `copy = [".env", ".envrc"]` — untracked-but-needed files copied
   (not symlinked — symlinks break Vite/Vitest resolution)
   from the main checkout into new trees.
4. A short "per-ecosystem recipes" docs page
   (Go: module cache is already global, nothing to do;
   pnpm: content-addressed store makes N trees cheap;
   npm/yarn: budget disk for N copies and lean on the hash gate).

**Alternatives:** wt-managed symlink/hardlink farms
(break module resolution; CoW still bottlenecks on directory-entry creation);
doing nothing (pool mode loses its reason to exist).

**Why:** hooks + hashing is the smallest mechanism capturing the whole win;
everything ecosystem-specific becomes the user's one-line command.
_Verdict: zealot wanted built-in pnpm/npm intelligence; maintainer overruled —
an unbounded compatibility surface._

### D6. Distribution: Homebrew tap via goreleaser + `go install` fallback

**Decision:** goreleaser on git tags via GitHub Actions,
publishing to a personal tap (`brew install logan/tap/wt`).
Use goreleaser's **`homebrew_casks`**, not `brews` —
the formula route was deprecated in goreleaser v2.10 (2025);
casks are the current vehicle for pre-built binaries.
Include the documented `postflight` quarantine-bit hook
until binaries are notarized (notarization is post-1.0).
`go install github.com/<owner>/wt/cmd/wt@latest` as the no-brew fallback.
No `curl | sh` script in v1.

**Uninstall:** `wt uninstall` performs no destructive action —
it prints the exact steps with real paths:
`brew uninstall wt`,
`rm -rf ~/.config/wt ~/.local/state/wt`,
and the eval line to delete from `~/.zshrc`.
`wt` never edits `.zshrc` on install either;
the README says "add this one line" (the zoxide/starship norm).

### D7. Branch freshness (~24h): opportunistic + explicit, no daemon

**Decision:** staleness = `last_fetch` timestamp older than
`staleness_hours` (default 24).
Enforced opportunistically where the user already expects latency:
`wt new` and pool claim fetch the base if stale (one-line stderr notice).
`wt ls`/`wt status` only _display_ staleness ("base fetched 2d ago"),
never touching the network.
Explicit `wt sync` fetches, fast-forwards the local base,
re-parks idle pool slots on the new tip, and reports per-tree behind-counts.
No launchd agent, no daemon;
docs include a copy-paste launchd plist for cron-style syncing, opt-in.
`wt` never rebases or touches branches carrying user commits.

**Alternatives:** Homebrew-style auto-update on every invocation
(surprise network I/O on read commands — rejected);
launchd by default (state mutating behind the user's back — rejected).

### D8. Versioning & release

**Decision:** SemVer.
Conventional commits with light enforcement (PR-title lint only).
Release notes come from news fragments, not commit messages:
every PR into `dev` stages one or more single-sentence
`.changes/<pr>.<type>.md` files
(types: enh, bug, dep, doc, maint;
created with `go run ./tools/changelog new`, enforced by CI),
a release batches them into `CHANGELOG.md`
(`go run ./tools/changelog batch <version>`),
and the release workflow extracts that section as the
GitHub Release notes (see CONTRIBUTING.md).
This keeps notes user-facing and merge-method independent,
so feature PRs may squash freely;
`CHANGELOG.md` itself is machine-folded, never hand-edited.
Version/commit/date embedded via ldflags, shown by `wt --version`.
Only `wt doctor` checks the GitHub releases API for updates —
an explicit command, so the network call is consented.

Pre-1.0 scheme: each phase exit tags `v0.1.0-alpha.N`,
exercising the whole release pipeline
(build, archive, changelog, GitHub prerelease, ldflags)
at every milestone,
while `skip_upload: auto` keeps alpha casks out of the Homebrew tap.
The real `v0.1.0` ships when the plan completes (Phase 7);
`v1.0.0` is earned afterwards by 0.1.x stability in real use,
not by the checklist finishing.

### D9. License: MIT

**Decision:** MIT.
**Alternatives:** Apache-2.0 (explicit patent grant — right for tools with
novel algorithms or big-co contribution ambitions).
**Why:** a thin git wrapper has no patent surface;
MIT is the overwhelming norm for Go CLI tools
and minimizes contributor friction. One file, done.

### D10. Go libraries (runtime dependency budget: 6)

Every pick was verified (July 2026) against maintenance status and against
what respected Go CLIs actually ship —
gh, git-town, goreleaser, glow, and gwq
(a git-worktree manager and the closest analog, which uses this exact
cobra + go-fuzzyfinder + lipgloss stack).

| Purpose                         | Library                             | Why over alternatives                                                                                                                                                                           |
| ------------------------------- | ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CLI framework                   | `github.com/spf13/cobra` (v1.10+)   | The de-facto standard — used by gh, git-town, goreleaser, gwq, glow; free zsh completion generation. urfave/cli v3 and kong are healthy but not better, and none of the surveyed CLIs use them. |
| Config                          | `github.com/pelletier/go-toml/v2`   | Actively maintained (v2.4.x, 2026); precise line/col error positions surface in `wt doctor`. BurntSushi/toml equally defensible; no reason to prefer it.                                        |
| Styling                         | `charm.land/lipgloss/v2`            | Color/layout without a framework; degrades to plain when stdout isn't a TTY. **v2 via the `charm.land` module path** — what gh/goreleaser/gwq ship; the github path is v1.                      |
| Interactive prompts (`wt init`) | `charm.land/huh/v2`                 | The current standard for CLI forms (gh depends on it). survey is archived (2024); promptui dead since 2021.                                                                                     |
| Fuzzy matching                  | `github.com/sahilm/fuzzy`           | Stable, zero-dependency, Sublime-style filename-optimized scoring; still a direct dependency of charmbracelet/bubbles — the ecosystem's endorsement. Alternatives are staler.                   |
| Interactive picker              | `github.com/ktr0731/go-fuzzyfinder` | fzf-like TUI as a library — no external fzf dependency, preview-pane support; exactly what gwq uses. Full bubbletea is overkill for one picker.                                                 |

Test-only: `github.com/rogpeppe/go-internal/testscript` (v1.15+),
`github.com/google/go-cmp`.
Lint: golangci-lint **v2** (`version: "2"` config format).
_Verdict: DX advocate wanted bubbletea everywhere; pragmatist ruled —
one picker does not justify an Elm runtime._

### D11. `cd` and shell integration: `eval "$(wt shell-init zsh)"`

**Decision:** a binary cannot change its parent's cwd,
so `wt shell-init zsh` emits:

1. a `wt()` wrapper function implementing the cd protocol
   (binary prints target path on stdout; function does the `cd`);
2. cobra-generated zsh completions;
3. an opt-in prompt hook: a zsh `chpwd` hook exporting `WT_PROMPT`
   from a live, builtins-only inspection of the tree's `.git` file —
   no git subprocess and no cache to go stale,
   recomputed only on cd, never per prompt render —
   plus a documented starship `custom`-segment alternative.

The prompt indicator ships as **optional**
(`wt init` asks; the eval line takes `--prompt`).
This is the verified current norm (zoxide, starship).

### D12. Bare `wt`: interactive picker

**Decision:** `wt` with no args opens the fuzzy picker over all trees
(branch, slot, age, dirty marker; preview pane shows `git status -sb`
plus last commit) and cd's to the selection.
When stdout is not a TTY it behaves as `wt ls --porcelain` instead,
so scripts and agents never hang on a TUI.

**Why:** the single most frequent intent is "take me to a tree";
making the best trick the default _is_ the beautiful-CLI requirement.
The TTY guard removes the agent-safety objection.
_Verdict: settled 2–2 by the tiebreak "optimize the most frequent action."_

### D13. Machine-output contract (core from v1)

Invariants enforced by the test harness on every command:
paths and porcelain data → stdout;
all human chatter, spinners, hints → stderr;
`--json` on `ls`, `status`, `doctor`;
`-q` silences stderr.
Exit codes: 0 ok, 1 error, 2 usage, 3 precondition failed
(dirty tree, no free slot, lease held), 4 not a wt repo.
Agents get correctness for free;
the opt-in Claude Code hook adapter is a post-1.0 phase (Phase 8).

### D14. Worktree locations, naming, and the two safety guards

**Decision:** default container is the sibling directory **`../<repo>.trees/`** —
default-mode trees at `../<repo>.trees/<sanitized-branch>`
(`/` → `-`; collision → explicit error with suggestion, never silent suffixing),
pool slots at `../<repo>.trees/pool-1…N`.
Configurable via `trees_dir`.
Siblings keep trees discoverable and on the same volume.

Safety inherits the reference tool's two guards verbatim:

1. **Pattern guard** — only paths matching the pool-slot pattern under the
   configured container can ever be reset or released;
   the main checkout and personal trees are structurally unresettable.
2. **Orphan-commit guard** — no reset/remove path ever discards commits
   reachable only from a detached HEAD
   (`git rev-list HEAD --not --all` must be empty, else refuse with
   recovery instructions).

### D15. Pool lease liveness (fixes the reference tool's known weakness)

**Decision:** a lease is an atomic `mkdir` under the state dir (kept —
portable, race-free), but the lease file records
`pid`, process start time, hostname, branch, and `claimed_at`.
A lease is stale only if the PID is dead
**or** its start time doesn't match (PID-reuse guard) —
never by wall clock alone.
The 24h idle heuristic applies only to released-but-dirty or
merged-branch slots, and `wt clean -n` previews every action.

---

## CLI Surface

| Command                          | One-liner                                                                                                        |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `wt`                             | Interactive fuzzy picker over trees → cd. Non-TTY: porcelain list.                                               |
| `wt init`                        | Interactive setup: base branch, trees dir, pool y/n + size, prompt indicator, copy list; writes `.git/wt.toml`.  |
| `wt new <branch> [--base <ref>]` | Default: create worktree + branch off base. Pool: claim a slot, reset, branch there. Prints tree path on stdout. |
| `wt ls [--json]`                 | List trees: branch, path, age, ahead/behind base, dirty, slot/lease state.                                       |
| `wt go [query]`                  | Fuzzy-jump: best match cd (with query) or picker (without).                                                      |
| `wt done [name] [--keep-branch]` | Finish a tree: safety checks, then remove (default) or release+reset slot (pool). Alias: `wt rm`.                |
| `wt sync [--all]`                | Fetch base, fast-forward it, re-park idle slots, report behind-counts. Never touches branches with user commits. |
| `wt clean [-n]`                  | Reap merged/stale trees and dead leases, `git worktree prune`, drop orphaned state entries.                      |
| `wt status [--json]`             | Repo overview: mode, base + fetch age, slot occupancy, per-tree disk usage.                                      |
| `wt doctor [--json]`             | Actionable diagnostics + update check. Exit 0 healthy / 3 issues found.                                          |
| `wt pool resize <n>`             | Grow (provision + setup hook) or shrink (free slots only) the pool.                                              |
| `wt pool ls`                     | Slot-centric view: free/claimed/by-whom/warm-since.                                                              |
| `wt config [--edit]`             | Print the active config paths and merged values; `--edit` opens the repo config in `$VISUAL`/`$EDITOR`.          |
| `wt path [name]`                 | Plumbing: print a tree's path (or the current tree's root).                                                      |
| `wt claim` / `wt release`        | Pool plumbing for scripts/agents: claim prints slot path on stdout.                                              |
| `wt shell-init zsh [--prompt]`   | Emit shim function, completions, optional prompt hook, for `eval` in `.zshrc`.                                   |
| `wt completion zsh`              | Raw completion script (also embedded in shell-init).                                                             |
| `wt uninstall`                   | Print exact removal steps for binary, config, state, and rc line.                                                |
| `wt --version`                   | Version, commit, date (ldflags).                                                                                 |

### Per-repo config — `.git/wt.toml`

```toml
base      = "green"                # base branch; default "main"
trees_dir = "../acme.trees"        # container for all wt-managed trees
copy      = [".env", ".envrc"]     # untracked files ported into new trees

[hooks]
setup              = "direnv exec . make bootstrap"  # once, on tree/slot creation
refresh            = "pnpm install"                  # on claim/new, gated by:
refresh_if_changed = ["pnpm-lock.yaml"]

[pool]                             # presence of this table = pool mode
size = 6
```

Global `~/.config/wt/config.toml` holds the same keys under `[defaults]`
plus `[ui] color = "auto"`.
Merge order: built-in → global → repo.

### State layout — `~/.local/state/wt/repos/<slug>-<hash8>/`

```
last_fetch                     # RFC3339 timestamp of last base fetch
leases/pool-3/lease.toml       # atomic-mkdir lease: pid, pid_start, branch, claimed_at
trees/<name>/refresh_hash      # SHA-256 of refresh_if_changed files at last refresh
trees/<name>/last_used         # idle heuristics and picker ranking
```

---

## Phases

Nine phases, each independently stoppable and resumable —
every exit criterion is a green, releasable main branch.
Sizes: S ≈ 1–2 days, M ≈ 3–4, L ≈ 5+.

Two standing rules apply to **every** phase:

1. **CI gates every PR from the first commit.**
   No feature code lands before the test/lint workflows exist,
   and branch protection on `main` requires them to pass.
2. **Docs move with the code.**
   Any phase that adds or changes a command updates the README table
   and the relevant `docs/` page in the same PR —
   Phase 7 is a dedicated editing pass, not the moment docs get written.

### Phase 1 — CI, release pipeline, walking skeleton (S)

CI and distribution are proven _before_ features pile up —
a test gate bolted on late never becomes cultural,
and a release pipeline bolted on late is where "trivial install" dies.

- **Entry:** empty repo.
- [x] **First commits are CI, not code:**
      `test.yml` (macos + ubuntu runners —
      ubuntu keeps portability honest for free)
      running `go test ./...` + `go vet`,
      `lint.yml` with golangci-lint v2,
      branch protection on `main` marking both as required checks —
      every subsequent PR must pass tests to merge.
      _(Protection applied to `main` and `dev`, 2026-07-18.)_
- [x] Scaffold: `cmd/wt/main.go`, `internal/cli/root.go` (cobra),
      MIT `LICENSE`, README stub with the macOS+zsh-only statement,
      empty `docs/` with placeholder index.
- [x] TDD the first real command:
      `internal/gitx` exec wrapper
      (test: parse `git worktree list --porcelain -z` fixtures) →
      `wt ls` (plain output).
- [x] `testscript` harness
      (`internal/cli/script_test.go` + `testdata/script/ls.txtar`):
      builds `wt`, runs it against a temp git repo created in-script.
- [x] `release.yml` running goreleaser on tags;
      `.goreleaser.yaml` with `homebrew_casks` → `homebrew-tap` repo;
      quarantine `postflight` hook; ldflags version embed.
- [x] Tag `v0.1.0-alpha.1`; verify:
      the GitHub prerelease exists with darwin archives
      and notes drawn from the batched news fragments,
      the archive binary runs (`wt ls`,
      `wt --version` shows the injected values),
      the rendered cask in `dist/` installs locally
      (`brew install --cask`, proving cask syntax
      and the quarantine hook),
      and a one-off manual push to `homebrew-tap` proves the PAT.
      The clean-machine `brew install <owner>/tap/wt` check
      runs at the real `v0.1.0` (Phase 7).
      _(Post-merge: needs the `loganthomas/homebrew-tap` repo,
      a `HOMEBREW_TAP_GITHUB_TOKEN` secret, then the tag.)_
- **Exit:** a proven release pipeline and one honest command;
  a PR cannot merge without green tests and lint.

**Status (2026-07-18): complete.**
`v0.1.0-alpha.1` tagged and released;
branch protection live on `main` and `dev`.

### Phase 2 — Core engine: config, init, new/done with safety guards (M)

- **Entry:** Phase 1 shipped.
- [x] `internal/config`: load/merge/validate;
      table-driven tests including error positions.
- [x] Repo identity: common-git-dir resolution, state-dir slug+hash
      (`internal/repo`).
- [x] **Safety guards before any destructive command exists** (`internal/guard`):
      dirty-tree, unpushed-commit, orphan-commit checks —
      unit-tested against fixture repos in every reachable state.
- [x] `wt init` (interactive form via huh v2, plus `--yes` and value flags
      for scriptability); `wt config [--edit]`.
- [x] `wt new` (branch create, sanitization, collision error,
      `copy` list, `hooks.setup`);
      `wt done`/`rm` (guards → `git worktree remove` →
      branch delete unless `--keep-branch`);
      `wt path`.
- [x] Exit-code + stdout/stderr contract enforced by a shared
      testscript assertion helper.
- **Exit:** full default-mode lifecycle usable day-to-day from the raw binary
  (no cd yet). Tag `v0.1.0-alpha.2`.

**Status (2026-07-20): complete.**
Merged to `main`; `v0.1.0-alpha.2` tagged and released.
Notable additions beyond the checklist:
`wt done` sweeps wt-planted `copy` files when their content still
matches the main checkout (an edited copy still trips the guard),
and the orphan check uses `--not --branches --tags --remotes`
because modern git's `--all` includes HEAD itself.
A pre-review hardening pass also landed:
repo-local `GIT_*` variables are scrubbed from every git call
and setup hook, so wt run from inside a git hook cannot be
retargeted at the hook's repository;
foreign process exit codes (git, hooks, editors) collapse to 1
instead of leaking through the D13 contract;
`wt.toml` writes are atomic;
tracked copy-list entries are left to git on both the plant
and sweep sides;
and `wt done` points prunable trees at `git worktree prune`.

### Phase 3 — Shell integration & navigation (M)

- [x] `wt shell-init zsh`: `go:embed` shim script —
      `wt()` function, cd protocol, completions;
      golden-file test of the emitted script plus a zsh smoke test
      (`zsh -c 'eval "$(wt shell-init zsh)"; wt go x'`) under testscript.
- [x] `wt go <query>`: sahilm/fuzzy over branch names + slot tickets;
      ambiguous → top-5 disambiguation on stderr, exit 3.
- [x] Bare `wt` and bare `wt go`: go-fuzzyfinder picker with preview pane;
      **non-TTY fallback to porcelain list**
      (testscript covers the non-TTY path;
      the picker itself gets a manual test note).
- [x] Optional prompt segment: chpwd-refreshed `WT_PROMPT`,
      `--prompt` flag on shell-init; starship recipe in docs.
- **Exit:** the eval line in `.zshrc` gives cd-on-select, completions,
  optional prompt. Tag `v0.1.0-alpha.3`.

**Status (2026-07-20):** code complete, PR open against `dev`.
Two D12 refinements surfaced by implementation:
the interactivity probe is **stdin + stderr**, never stdout —
the shim captures stdout to implement the cd protocol,
so a stdout check would make the picker unreachable
(the picker renders on `/dev/tty`; the porcelain fallback for
scripts and agents is unchanged) —
and the porcelain fallback landed as an explicit
`wt ls --porcelain` flag so scripts can ask for it by name.
Fuzzy ranking normalizes away the matcher's name-length penalty:
a jump is decided by match quality alone,
so `feature` refuses to pick `feature/login` over `feature/logout`
just because it is a letter shorter.
The shim's cd set is bare `wt`, `wt go`, and `wt new`;
`wt path` stays cd-free plumbing.
Completions are bootstrapped via `eval "$(wt completion zsh)"`
inside the shim rather than inlined,
so they track the installed binary and golden files stay stable.
Remaining before exit is met: merge, batch fragments,
tag `v0.1.0-alpha.3`.
Phase 4 (pool mode) is ready to be taken up once the tag is cut.

### Phase 4 — Pool mode (L)

- [ ] `internal/lease`: atomic-mkdir lease + PID/start-time liveness —
      TDD before claim exists
      (unit tests simulate a crash: lease held by a dead PID).
- [ ] Slot provisioning: `wt init` pool path and `wt pool resize`
      (grow runs the setup hook per slot; shrink refuses claimed slots).
- [ ] Claim/reset semantics (from the reference tool):
      `git fetch` (if stale) → `checkout -f --detach <base>` →
      `clean -fd` (**never `-x`** — gitignored build artifacts keep slots warm) →
      refresh-hash gate → `hooks.refresh` if lockfiles changed → branch create.
- [ ] Pattern guard: reset/release refuse any path not matching
      `<trees_dir>/pool-N` — unit-tested with hostile inputs
      (main checkout, personal tree, symlinks).
- [ ] `wt new`/`done` dispatch on pool presence;
      `wt claim`/`release` plumbing; `wt pool ls`.
- [ ] Fuzzy matching over slots targets the _branch/ticket_, not `pool-3`
      (picker shows `PROJ-123 → pool-3`).
- **Exit:** claim → work → release loop with crash-safe leases and
  warm-cache resets. Tag `v0.1.0-alpha.4`.

### Phase 5 — Sync & freshness (M)

- [ ] `last_fetch` staleness core; display in `ls`/`status`.
- [ ] Opportunistic fetch on `new`/claim with stderr notice;
      `--no-fetch` escape hatch.
- [ ] `wt sync [--all]`: fetch, ff-only base update,
      re-park idle slots with gated refresh, behind-count report.
      Testscript: a local "origin" fixture advanced by the test;
      assert slots re-park and a slot with user commits is untouched.
- [ ] Docs: opt-in launchd plist recipe.
- **Exit:** the 24h freshness window holds with zero daemons.
  Tag `v0.1.0-alpha.5`.

### Phase 6 — Doctor, clean, status (M)

- [ ] `wt clean`: dead-lease reap, merged-branch tree reap
      (ancestor-of-base check), `git worktree prune`,
      orphaned state-dir GC; `-n` dry-run; every action printed.
- [ ] `wt status`: mode, base + fetch age, occupancy,
      per-tree disk usage (parallelized `du`, cached in state dir).
- [ ] `wt doctor` checks, each formatted symptom → cause → exact fix command:
      git ≥ 2.38; shim installed and current; config parse errors with
      line numbers; prunable/locked worktrees; branch-checked-out-twice;
      stale leases; submodules present (warn);
      `core.hooksPath`/husky note; trees on a different volume;
      update check (prerelease tags ignored when comparing versions).
- [ ] `--json` for status/doctor/ls via a single `internal/render` layer
      so human and JSON views can't drift.
- **Exit:** self-diagnosing tool —
  support questions answerable with "run `wt doctor`."
  Tag `v0.1.0-alpha.6`.

### Phase 7 — Documentation, polish, first release (L)

Docs have been written alongside each phase (standing rule 2);
this phase is the editorial pass that makes them _good_ —
clear, concise, never bloated —
plus final UX polish.

**Documentation structure** (two tiers, deliberately split so the README
stays short):

- **Root `README.md`** — the basics only, top-fold-first:
  what wt is (3 sentences + one screenshot/gif),
  install (brew one-liner + the eval line),
  the 60-second walkthrough (`init → new → go → done`),
  a compact command table,
  uninstall,
  and links into `docs/`.
  Hard budget: readable end-to-end in under 5 minutes.
- **`docs/`** — the extensive material, one focused page each:
  - `docs/pool-mode.md` — monorepo setup, slots, claim/release lifecycle,
    resize, worked example with real hook config.
  - `docs/configuration.md` — full config reference
    (every key, default, and merge order), state-dir layout.
  - `docs/recipes.md` — per-ecosystem examples
    (Go, pnpm, npm/yarn, direnv, `.env` porting, port-per-slot pattern).
  - `docs/shell.md` — shim internals, prompt indicator, starship segment,
    completions.
  - `docs/agents.md` — machine contract: stdout/stderr rules, exit codes,
    `--json` schemas, `claim`/`release` plumbing (expanded in Phase 8).
  - `docs/faq.md` — the doctor-adjacent questions
    (submodules, hooksPath, locked trees, disk usage).

**Tasks:**

- [ ] Write/edit the docs set above;
      editorial pass with a hard rule per page:
      every section must answer a question a real user has —
      delete anything that doesn't.
- [ ] README rewrite to the two-tier structure;
      verify the 60-second walkthrough by literally following it on a
      clean machine, timing it.
- [ ] lipgloss pass over all human output, golden-file tested
      (`NO_COLOR` and width-degradation cases);
      help-text audit — every `--help` fits one screen and links its
      `docs/` page.
- [ ] `wt uninstall`; end-to-end `brew uninstall` re-verification.
- [ ] Failure-message audit: every error names the fix
      (`error: branch 'x' already checked out in ../acme.trees/x — wt go x?`).
- [ ] Cut `v0.1.0` — the first real release:
      the cask publishes to the tap;
      clean-machine `brew install <owner>/tap/wt && wt ls && wt --version`,
      and `brew uninstall` leaves nothing behind.
- **Exit:** a stranger installs, inits, and ships a branch
  without reading past the README's top fold;
  everything deeper is one `docs/` link away.
  `v1.0.0` is not a phase deliverable:
  it is cut later, once 0.1.x has soaked in real use
  and the command surface has stopped moving (see D8).

### Phase 8 — Post-release: agent integration, opt-in (M)

- [ ] Claude Code hook adapter wiring `wt claim`/`release`
      to session start/stop; `wt new --json` envelope;
      "Using wt with agents" docs page.
- Design is already guaranteed by D13 — this phase is packaging, not plumbing.

### Phase 9 — Post-1.0: portability (L)

- [ ] Linux + bash/fish shims (shell-init is already parameterized by shell);
      promote Linux CI from build-only to full;
      archive/`.deb` targets in goreleaser;
      macOS notarization.

---

## Risks & Pitfalls Register

| #   | Risk                                                                        | Mitigation                                                                                                               | Phase |
| --- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ | ----- |
| R1  | A binary can't cd its parent shell                                          | Shim + cd protocol; every cd-producing command also works shimless (prints path)                                         | 3     |
| R2  | A reset destroys work — the existential risk                                | Pattern guard + orphan-commit guard + dirty/unpushed checks; guards built and tested _before_ destructive commands exist | 2, 4  |
| R3  | Crashed process leaks a lease forever / long-running legit work gets reaped | PID + start-time liveness; wall-clock heuristics only for released slots                                                 | 4     |
| R4  | Branch already checked out in another worktree (git hard error)             | Pre-check in `new`; doctor detects; error points at the existing tree                                                    | 2, 6  |
| R5  | Submodules × worktrees are notoriously buggy                                | Detect at `init`, warn "supported by git, not smoothed by wt"; no wt machinery                                           | 6     |
| R6  | `.env`/untracked files don't follow to new trees; symlinks break Vite       | `copy` list with copy semantics, set at init                                                                             | 2     |
| R7  | `core.hooksPath`/husky relative-path quirks per worktree                    | Doctor check + docs (absolute-path advice)                                                                               | 6     |
| R8  | `git worktree prune` vs wt state drift                                      | `wt clean` reconciles both ways (git metadata ↔ state dir)                                                               | 6     |
| R9  | Name collisions / slashed branches / long paths                             | Sanitization + explicit collision errors; container dir keeps paths short                                                | 2     |
| R10 | Disk bloat from N node_modules copies                                       | `wt status` per-tree sizes; pool caps N by design; recipes doc                                                           | 5, 6  |
| R11 | Shared object store: gc/repack while trees are in use                       | Docs note; `wt clean` never triggers gc; watch-item, not v1 machinery                                                    | 6     |
| R12 | Locked worktrees (`git worktree lock`)                                      | `ls` shows the lock flag; `done` refuses locked trees with an explanation                                                | 6     |
| R13 | Unsigned-binary quarantine on macOS                                         | Cask `postflight` xattr hook; notarization post-1.0                                                                      | 1     |
| R14 | Prompt hook slows every prompt render                                       | chpwd hook of zsh builtins only — no subprocess, recomputed on cd not per render; feature optional                        | 3     |
| R15 | Picker hangs agents/scripts                                                 | Non-TTY → porcelain, enforced by testscript                                                                              | 3     |
| R16 | IDE state (VS Code) doesn't follow trees                                    | Docs: `code $(wt path <name>)`; out of scope for v1                                                                      | 7     |
| R17 | Dev-server port collisions across parallel trees                            | Docs pattern: derive port from slot number in `.envrc`; not wt machinery                                                 | 7     |
| R18 | Name clash with existing `wt`/`wtp`/`wt-cli` tools                          | Tap-scoped cask avoids brew collision; doctor detects a shadowing `wt` on PATH                                           | 1, 6  |
| R19 | `extensions.worktreeConfig` interactions                                    | wt never enables it in v1; doctor notes it if enabled and something looks off                                            | 6     |

---

## Verification Approach

- **Every phase:** `go test ./...` on macOS + ubuntu CI; `golangci-lint`;
  the testscript e2e suite grows monotonically —
  each command lands with its `.txtar`.
- **Test pyramid:**
  table-driven unit tests for pure logic
  (guards, config merge, fuzzy ranking, lease liveness) →
  testscript integration against real temp git repos with a scripted origin
  (fixture helpers create dirty, unpushed, detached-with-orphans,
  and merged states) →
  golden files for all human output (`-update` regeneration, `NO_COLOR` variants).
- **Release verification:** every phase exit tags an alpha
  that must produce a working prerelease (Phase 1 defines the checks);
  the clean-machine
  `brew install → init → new → go → done → uninstall` checklist,
  scripted where possible, runs at `v0.1.0` (Phase 7).
- **Contract tests:** one shared harness asserting stdout purity,
  exit codes, and `--json` schema stability for every command —
  regression-proofing the agent contract from Phase 2 onward.
- **Pool soak (Phase 4):** 20 interleaved claim/release cycles with a process
  killed mid-claim; assert no lost slots, no double-claims,
  no reset of any non-slot path.

---

## Research Sources

- goreleaser: [homebrew_casks](https://goreleaser.com/customization/publish/homebrew_casks/), [deprecations](https://goreleaser.com/resources/deprecations/), [v2.10 announcement](https://goreleaser.com/blog/goreleaser-v2.10/)
- [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) and write-ups (Encore, rednafi)
- go-git worktree limitations: [issue #1956](https://github.com/go-git/go-git/issues/1956), experimental [x/plumbing/worktree](https://pkg.go.dev/github.com/go-git/go-git/v6/x/plumbing/worktree)
- Comparable tools surveyed: [wtp](https://github.com/satococoa/wtp), [gwq](https://github.com/d-kuro/gwq), [wt-cli](https://github.com/bkildow/wt-cli), zoxide/starship/direnv (shell-init pattern), git-town (Go git-wrapper precedent)
- [daveschumaker.net worktrees post](https://daveschumaker.net/use-git-worktrees-they-said-itll-be-fun-they-said/) (pool + lockfile-gate pattern)
