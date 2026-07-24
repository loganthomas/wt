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
| `wt claim`  | The claimed slot's absolute path, one line. No free slot: exit 3. |
| `wt release` | Nothing (chatter on stderr). Not a slot / not claimed: exit 3. |
| `wt pool ls` | One aligned row per slot: slot, state (`free`, `claimed`, `stale`, `unprovisioned`), branch, detail. |

The claim/release loop for agents
(see [pool-mode.md](pool-mode.md)):

```sh
slot="$(wt claim "$TICKET")"       # a warm slot in seconds
cd "$slot" && …work…
wt release "$TICKET"               # branch survives for the PR flow
```

`--json` on `ls`/`status`/`doctor` lands in a later phase.

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
