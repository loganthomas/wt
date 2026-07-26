# Scripting and agents: the machine contract

`wt` treats machine-readable output as a first-class contract
(PLAN.md D13). Scripts, CI, and coding agents rely on exactly
these rules — they hold for every command, enforced by the test
suite.

## Streams

- **stdout** carries data: paths, porcelain listings, config.
  Nothing else, ever.
- **stderr** carries everything meant for humans:
  progress notes, hook output, hints, errors.

So this is always safe:

```sh
tree="$(wt new feature/login)"     # the tree's absolute path, nothing else
cd "$tree"
```

## Exit codes

| Code | Meaning                                                              |
| ---- | -------------------------------------------------------------------- |
| 0    | Success.                                                             |
| 1    | Error (git failure, unknown tree, invalid config).                   |
| 2    | Usage: unknown command, bad flag, wrong arguments.                   |
| 3    | Precondition failed: dirty tree, unpushed commits, name collision, already initialized. The repo is fine; the state blocks the action. |
| 4    | Not inside a git repository.                                         |

Code 3 is the one worth branching on: it means "resolve something,
then retry", never "wt is broken".

## Per-command notes

| Command     | stdout                                        |
| ----------- | --------------------------------------------- |
| `wt new`    | The new tree's absolute path, one line.       |
| `wt path`   | The resolved tree's absolute path, one line.  |
| `wt go <q>` | The matched tree's absolute path, one line. Ambiguous: contenders on stderr, exit 3. No match: exit 1. |
| `wt ls`     | One aligned row per tree.                     |
| `wt ls --porcelain` | One tree per line, three tab-separated fields: branch label, absolute path, comma-joined states (`-` when none). The field count never varies. |
| bare `wt`, bare `wt go` | Without a TTY on stdin and stderr: exactly the `--porcelain` listing, so agents never hang on the interactive picker. |
| `wt shell-init zsh` | The zsh integration script itself (it is the machine output — meant for `eval`). |
| `wt config` | Merged config as TOML; the two config file paths ride along as `#` comments, so the whole document stays parseable TOML. |
| `wt init`   | Nothing (chatter on stderr). Non-interactive use requires `--yes` plus value flags; without a TTY, prompting is refused (exit 2) rather than hanging. |
| `wt done`   | Nothing (chatter on stderr).                  |
| `wt sync`   | Nothing (fetch, fast-forward, and per-tree behind report all ride stderr). |
| `wt claim`  | The claimed slot's absolute path, one line. No free slot: exit 3. |
| `wt release` | Nothing (chatter on stderr). Not a slot / not claimed: exit 3. |
| `wt pool ls` | One aligned row per slot: slot, state (`free`, `claimed`, `stale`, `unprovisioned`), branch, detail. |
| `wt clean`  | Nothing (every action, and `-n`'s previews, ride stderr). |
| `wt status` | The overview table; `--json` for the machine shape below. |
| `wt doctor` | The check report; `--json` for the machine shape below. Exit 0 healthy, 3 when a `fail`-status check needs fixing; `warn`/`info` are advisory and never exit 3. Works outside a repository (repo checks simply absent). |

The claim/release loop for agents
(see [pool-mode.md](pool-mode.md)):

```sh
slot="$(wt claim "$TICKET")"       # a warm slot in seconds
cd "$slot" && …work…
wt release "$TICKET"               # branch survives for the PR flow
```

## `--json`

`ls`, `status`, and `doctor` take `--json`: two-space-indented
JSON on stdout, nothing on stderr. Fields marked *omitempty* are
absent when zero/empty. Free-text fields (`note`, `symptom`,
`cause`, `fix`) are for humans and may reword between releases;
every other field name and value is stable.

`wt ls --json` — an array of trees:

```json
[
  {
    "branch": "feature/login",
    "path": "/Users/you/acme.trees/feature-login",
    "head": "0b7e…",
    "detached": false,
    "locked": true,
    "locked_reason": "keep me",
    "prunable": false
  }
]
```

`wt status --json` — the repo overview:

```json
{
  "mode": "pool",
  "base": { "name": "main", "last_fetch": "2026-07-25T09:30:00Z", "stale": false },
  "trees": [
    { "branch": "main", "path": "/Users/you/acme", "disk_kb": 1782579 }
  ],
  "pool": {
    "size": 2,
    "slots": [
      { "slot": "slot-1", "state": "claimed", "branch": "PROJ-123",
        "pid": 4242, "claimed_at": "2026-07-25T08:00:00Z", "note": "…" },
      { "slot": "slot-2", "state": "free" }
    ]
  }
}
```

`base.last_fetch` is absent until wt has fetched once;
`disk_kb` is absent when a tree could not be measured
(sizes are cached for up to an hour);
`pool` is absent in default mode.

`wt doctor --json` — the diagnostics:

```json
{
  "checks": [
    { "name": "git", "status": "ok", "symptom": "2.50.1" },
    { "name": "worktrees", "status": "fail",
      "symptom": "1 registered tree gone from disk",
      "cause": "a tree directory was deleted without telling git",
      "fix": "wt clean" }
  ],
  "issues": 1
}
```

`status` is one of `ok`, `info`, `warn`, `fail`;
`issues` counts the fails and matches the exit code rule above.
Check names are stable identifiers: `git`, `shell-shim`,
`repo` (only when the repository itself cannot be resolved),
`config`, `worktrees`, `branches`, `submodules`, `hooks-path`,
`trees-volume`, `leases` (pool repos), `update`.

A porcelain line looks like:

```
feature/login	/Users/you/acme.trees/feature-login	-
main	/Users/you/acme	locked,prunable
```

Detached trees carry the literal branch label `(detached)`.

## Hooks

`hooks.setup` runs via `sh -c` inside the new tree.
Both its stdout and stderr are forwarded to wt's **stderr**,
so a chatty install script can never corrupt the contract above.
