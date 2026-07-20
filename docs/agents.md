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
| `wt ls`     | One aligned row per tree.                     |
| `wt config` | Merged config as TOML; the two config file paths ride along as `#` comments, so the whole document stays parseable TOML. |
| `wt init`   | Nothing (chatter on stderr). Non-interactive use requires `--yes` plus value flags; without a TTY, prompting is refused (exit 2) rather than hanging. |
| `wt done`   | Nothing (chatter on stderr).                  |

`--json` on `ls`/`status`/`doctor` and the pool plumbing
(`wt claim` / `wt release`) land in later phases.

## Hooks

`hooks.setup` runs via `sh -c` inside the new tree.
Both its stdout and stderr are forwarded to wt's **stderr**,
so a chatty install script can never corrupt the contract above.
