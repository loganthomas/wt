# Configuration

`wt` reads two optional TOML files and merges them over built-in
defaults, most specific last:

1. **Built-in defaults** — `base = "main"`, trees in `../<repo>.trees`.
2. **Global** — `~/.config/wt/config.toml`
   (`$XDG_CONFIG_HOME/wt/config.toml` if set):
   your personal defaults for every repo, under `[defaults]`.
3. **Per-repo** — `<git-common-dir>/wt.toml`
   (that is `.git/wt.toml` in the main checkout),
   written by `wt init`.

Scalars replace when set; lists replace as a whole.
`wt config` prints both paths and the merged result;
`wt config --edit` opens the repo file in `$VISUAL` (or `$EDITOR`)
and validates it on save.

The repo config lives *inside* `.git` on purpose:
every linked worktree resolves to the same file,
`git clean` can never delete it,
and collaborators never see it in `git status`.
It is per-person, per-machine config — like `.git/config` itself.

## Per-repo file — `.git/wt.toml`

Written by `wt init`, which pre-fills its answers
from a scan of the repo root — see
[detected defaults](#detected-defaults) below.

```toml
base      = "main"                 # branch new trees start from
trees_dir = "../acme.trees"        # container for wt-managed trees
copy      = [".env", ".envrc"]     # untracked files copied into new trees

[hooks]
setup              = "make bootstrap"   # runs once, inside each new tree/slot
refresh            = "pnpm install"     # runs on each claim — but only
refresh_if_changed = ["pnpm-lock.yaml"] # …when these files' hash changed

[pool]                             # presence of this table = pool mode
size = 6
```

| Key                        | Default            | Meaning                                                                                                                       |
| -------------------------- | ------------------ | ----------------------------------------------------------------------------------------------------------------------------- |
| `base`                     | `main`             | Base branch `wt new` branches off (override per call with `--base`).                                                          |
| `trees_dir`                | `../<repo>.trees`  | Container for managed trees. Relative paths anchor at the main checkout, so they mean the same thing from any worktree.       |
| `copy`                     | `[]`               | Untracked files ported (copied, never symlinked) from the main checkout into each new tree. Entries must stay inside the tree. |
| `hooks.setup`              | —                  | Command run once inside a freshly created tree or provisioned slot, via `sh -c`. Its output goes to stderr.                   |
| `hooks.refresh`            | —                  | Command run on every claim, and on `wt new` when no setup hook is configured (setup is presumed to leave the tree fully built). Gated by `refresh_if_changed`; without a gate it runs each time. |
| `hooks.refresh_if_changed` | `[]`               | Files whose combined hash gates `hooks.refresh`: unchanged hash, no run. The lockfile short-circuit of [pool mode](pool-mode.md). |
| `pool.size`                | —                  | Number of pre-warmed slots; the `[pool]` table's presence is what enables [pool mode](pool-mode.md). Resize with `wt pool resize`. |

### Detected defaults

Users rarely know what "warm" means for their tree in wt's terms,
but their lockfiles do.
So `wt init` scans the repo root and pre-fills the form
(or, under `--yes`, the file it writes) from what it finds.
It only ever proposes: every value stays editable.

**Refresh hook.** The first tracked marker present wins,
most specific first:

| Marker              | Proposed `hooks.refresh` |
| ------------------- | ------------------------ |
| `pnpm-lock.yaml`    | `pnpm install`           |
| `package-lock.json` | `npm ci`                 |
| `yarn.lock`         | `yarn install`           |
| `uv.lock`           | `uv sync`                |
| `poetry.lock`       | `poetry install`         |
| `Cargo.lock`        | `cargo fetch`            |

The winning marker also becomes `hooks.refresh_if_changed`,
so the hook re-runs only when that lockfile actually moves.
The marker must be **tracked**:
an untracked lockfile never reaches a fresh tree,
so a gate on it would hash as absent forever
and the hook would run once and then never again.

**Copy list.** `.env`, `.envrc`, and `.env.local` are proposed
when present and **untracked** — the opposite rule,
because a tracked `.env` already travels with every checkout
and needs no copying.

**`hooks.setup` is never proposed.**
A real bootstrap (`make bootstrap`, `bazel build //...`)
is exactly what a scan cannot guess,
and on a fresh tree the refresh hook runs anyway.

Three layers settle each value, most explicit first:

1. **Flags** — `--refresh`, `--refresh-if-changed`, `--copy`, `--setup`.
2. **Global config** — `~/.config/wt/config.toml`.
3. **The scan.**

wt prints one stderr line per proposal that actually landed,
so it never advertises a value a flag or your global config then beat:

```
$ wt init --yes
detected pnpm-lock.yaml — proposing refresh hook "pnpm install" gated on it
detected untracked .env — proposing it for the copy list
initialized wt (default mode) — config at /path/to/repo/.git/wt.toml
```

An explicitly empty flag declines a proposal outright:
`wt init --yes --refresh ''` writes no refresh hook,
and drops the gate along with it
(a gate with no hook would describe a run that cannot happen).
It cannot unset a *global* default, though:
by the merge rules above an empty value never overrides,
so remove it from the global file instead.

### Copied files and `wt done`

Files planted by `copy` are untracked by design.
`wt done` tolerates them as long as their content still matches the
main checkout — wt planted them, wt sweeps them.
A copy that no longer matches counts as real work:
wt's own copy check refuses removal until you back it up or
re-sync it, even when the file is gitignored and invisible
to `git status`.

## Global file — `~/.config/wt/config.toml`

The same keys, under `[defaults]`, plus a `[ui]` section:

```toml
[defaults]
base = "trunk"
copy = [".env"]

[ui]
color = "auto"        # auto | always | never
```

`[pool]` is deliberately not accepted under `[defaults]`:
pool mode is a per-repo decision (PLAN.md D3),
and a global pool would silently flip every repo into it.

## Errors

Config errors carry file, line, and column:

```
$ wt config
wt: /path/.git/wt.toml:2:1: unknown key "bogus" (not part of wt's config)
```

## State layout — `~/.local/state/wt/repos/<slug>-<hash8>/`

`wt` keeps its bookkeeping out of your repo,
under `$XDG_STATE_HOME` (default `~/.local/state`):

```
wt/repos/acme-3f2a9c1b/          # <repo basename>-<hash of the git dir path>
  leases/slot-3/lease.toml       # who holds slot 3: pid, start time, branch
  trees/<name>/refresh_hash      # refresh_if_changed hash at last refresh
  trees/<name>/provisioned       # slot finished provisioning (setup ran)
```

The slug keeps state directories human-readable;
the hash keeps two clones named `acme` apart.
Fetch timestamps join the layout in Phase 5.
