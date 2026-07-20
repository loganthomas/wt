# Changelog

## v0.1.0-alpha.3 - 2026-07-20

### Enhancements

- One `eval "$(wt shell-init zsh)"` line in `.zshrc` makes bare `wt` an interactive tree picker, `wt go` a fuzzy cd, and `wt new` land in the fresh tree — with zsh completions, an opt-in `WT_PROMPT` indicator, and a script-safe `wt ls --porcelain` fallback whenever no TTY is present. (#8)

### Documentation

- A shell-integration docs page covers the cd protocol, fuzzy-matching rules, completions, and the `WT_PROMPT`/starship prompt recipes. (#8)

### Infrastructure

- The ubuntu CI runner installs zsh, so the shell-integration smoke tests run on both platforms. (#8)

## v0.1.0-alpha.2 - 2026-07-20

### Enhancements

- `wt init`, `wt new`, `wt done`/`wt rm`, `wt path`, and `wt config` deliver the full default-mode worktree lifecycle, guarded by dirty/unpushed/orphan safety checks and exact exit codes. (#5)

### Fixes

- Alpha releases publish their notes instead of an empty body. (#4)
- Repo-local `GIT_*` variables are scrubbed from every git call and setup hook, so wt run from inside a git hook acts on its own repository instead of the hook's.

### Documentation

- New docs pages cover the configuration reference and the machine-output contract for scripts and agents. (#5)

## v0.1.0-alpha.1 - 2026-07-19

### Enhancements

- `wt ls` lists every worktree with branch, path, and state; `wt --version` reports the build. (#1)

### Documentation

- README install and requirements guidance, a contribution guide, and the project plan with its alpha-tagged release roadmap. (#1)

### Infrastructure

- CI test and lint gates, a mirroring pre-commit hook, issue forms, the goreleaser release pipeline with Homebrew tap plumbing, and the news-fragment changelog workflow with its PR gate. (#1)
