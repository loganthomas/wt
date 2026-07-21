# wt

`wt` — short for **worktree** — is a thin, elegant Go wrapper
around `git worktree`.
Create, list, jump to, and safely remove worktrees with sane paths —
and, for monorepos where cold worktrees are unusable,
an opt-in pool of pre-warmed, reusable slots.

> **Status: pre-1.0, under active development.**
> **v1 targets macOS + zsh only.**
> The design doesn't preclude Linux/bash/fish; v1 simply doesn't go there.

## Requirements

| Dependency | Minimum | Notes                                                  |
| ---------- | ------- | ------------------------------------------------------ |
| macOS      | —       | v1 scope, zsh as the shell                             |
| git        | ≥ 2.38  | `wt` shells out to your git                            |
| Go         | ≥ 1.25  | only to build from source; the brew install needs none |

## Install

Coming with the first release:

```sh
brew install loganthomas/tap/wt
```

Fallback, no Homebrew required:

```sh
go install github.com/loganthomas/wt/cmd/wt@latest
```

Then wire up the shell integration —
cd-on-select, completions, optional prompt indicator —
with one line in `~/.zshrc`:

```sh
eval "$(wt shell-init zsh)"
```

Details in [docs/shell.md](docs/shell.md).

## Commands

| Command                          | One-liner                                                                     |
| -------------------------------- | ----------------------------------------------------------------------------- |
| `wt`                             | Interactive fuzzy picker over trees → cd. Without a TTY: porcelain list.      |
| `wt init`                        | Set up wt for a repo (prompts, or `--yes` + flags); writes `.git/wt.toml`.    |
| `wt new <branch> [--base <ref>]` | Create a worktree + branch off the base, and cd there under the shim. In [pool mode](docs/pool-mode.md): claim a pre-warmed slot instead. |
| `wt ls [--porcelain]`            | List worktrees: branch, path, state.                                          |
| `wt go [query]`                  | Fuzzy-jump: best match cds (with a query) or picker (without).                |
| `wt done [name] [--keep-branch]` | Finish a tree: safety checks, remove it, delete its branch. Alias: `wt rm`.   |
| `wt claim <branch>`              | Pool mode: claim a slot for a branch; slot path on stdout (plumbing).         |
| `wt release [name]`              | Pool mode: park a slot back on the base, keeping its branch (plumbing).       |
| `wt pool ls`                     | Pool mode: slot-centric view — free, claimed, by whom.                        |
| `wt pool resize <n>`             | Pool mode: grow (provision + warm) or shrink (free slots only).               |
| `wt path [name]`                 | Print a tree's absolute path (plumbing).                                      |
| `wt config [--edit]`             | Show active config paths and merged values; `--edit` opens `$VISUAL`/`$EDITOR`. |
| `wt shell-init zsh [--prompt]`   | Emit the shim, completions, and optional prompt hook for eval in `.zshrc`.    |
| `wt --version`                   | Version, commit, build date.                                                  |

The full surface (`sync`, `clean`, `status`, …)
lands phase by phase; see [PLAN.md](PLAN.md).
Monorepo pool mode: [docs/pool-mode.md](docs/pool-mode.md).
Configuration reference: [docs/configuration.md](docs/configuration.md).
Scripting and agent contract: [docs/agents.md](docs/agents.md).

## Docs

Deeper material lives in [docs/](docs/).

## Developing

After cloning, point git at the repo's hooks
so every commit runs the same checks CI does:

```sh
git config core.hooksPath .githooks
```

## License

[MIT](LICENSE)
