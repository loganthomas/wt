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

```toml
base      = "main"                 # branch new trees start from
trees_dir = "../acme.trees"        # container for wt-managed trees
copy      = [".env", ".envrc"]     # untracked files copied into new trees

[hooks]
setup              = "make bootstrap"   # runs once, inside each new tree
refresh            = "pnpm install"     # pool mode (Phase 4): runs on claim,
refresh_if_changed = ["pnpm-lock.yaml"] # …only when these files' hash changed

[pool]                             # presence of this table = pool mode
size = 6
```

| Key                        | Default            | Meaning                                                                                                                       |
| -------------------------- | ------------------ | ----------------------------------------------------------------------------------------------------------------------------- |
| `base`                     | `main`             | Base branch `wt new` branches off (override per call with `--base`).                                                          |
| `trees_dir`                | `../<repo>.trees`  | Container for managed trees. Relative paths anchor at the main checkout, so they mean the same thing from any worktree.       |
| `copy`                     | `[]`               | Untracked files ported (copied, never symlinked) from the main checkout into each new tree. Entries must stay inside the tree. |
| `hooks.setup`              | —                  | Command run once inside a freshly created tree, via `sh -c`. Its output goes to stderr.                                       |
| `hooks.refresh`            | —                  | Pool mode, lands in Phase 4.                                                                                                  |
| `hooks.refresh_if_changed` | `[]`               | Pool mode, lands in Phase 4.                                                                                                  |
| `pool.size`                | —                  | Number of pre-warmed slots; the `[pool]` table's presence is what enables pool mode. Lands in Phase 4.                        |

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
wt/repos/acme-3f2a9c1b/     # <repo basename>-<hash of the git dir path>
```

The slug keeps state directories human-readable;
the hash keeps two clones named `acme` apart.
Phase 2 defines the location; leases, fetch timestamps,
and refresh hashes fill it in as later phases land.
