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

## Commands

| Command        | One-liner                            |
| -------------- | ------------------------------------ |
| `wt ls`        | List worktrees: branch, path, state. |
| `wt --version` | Version, commit, build date.         |

The full surface (`init`, `new`, `go`, `done`, `sync`, pool mode, …)
lands phase by phase; see [PLAN.md](PLAN.md).

## Docs

Deeper material lives in [docs/](docs/).

## Developing

## License

[MIT](LICENSE)
