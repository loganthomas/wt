# Shell integration

One line in `~/.zshrc` turns `wt` from a path-printing binary
into a navigator:

```sh
eval "$(wt shell-init zsh)"
```

Add `--prompt` to also get the [prompt indicator](#prompt-indicator).
`wt` never edits `.zshrc` for you, and `wt uninstall` (Phase 7)
tells you the exact line to delete.

## The cd protocol

A child process cannot change its parent shell's directory,
so the binary and the shim split the work
(the zoxide/starship pattern):
the binary prints the target path on stdout,
and the `wt()` function the eval line installed performs the `cd`.

Only three invocations produce a cd —
bare `wt`, `wt go`, and `wt new`:

```sh
wt                    # pick a tree interactively → cd
wt go login           # fuzzy-jump to the best match → cd
wt new feature/pay    # create tree + branch → cd into it
```

Every other command passes through the wrapper untouched.
`wt path` deliberately never cds — it is plumbing,
built for things like `code "$(wt path login)"`.

Without the shim everything still works;
cd-producing commands simply print the path instead:

```sh
cd "$(wt go login)"
```

## Fuzzy matching rules

`wt go <query>` matches against each tree's branch name,
its sanitized directory form (`feature/login` → `feature-login`),
and its directory basename:

1. An exact spelling wins outright, branch names before directory names.
2. Otherwise the best fuzzy match wins —
   but only when it strictly beats the runner-up on match quality.
3. A tie is ambiguous: the contenders are listed on stderr
   and `wt` exits 3 without guessing.

Match quality ignores name length,
so `wt go feature` refuses to choose between
`feature/login` and `feature/logout`
rather than "winning" on the shorter name.

## The picker

Bare `wt` (and bare `wt go`) opens a fuzzy picker over all trees;
the preview pane shows each tree's `git status -sb` and last commit.
Enter cds to the selection, Esc cancels.

The picker only appears when stdin and stderr are TTYs.
Anything piped or captured — scripts, CI, coding agents —
gets the stable `wt ls --porcelain` listing on stdout instead,
so nothing ever hangs waiting for a human
(see [agents.md](agents.md)).

The picker TUI itself is verified by hand
(create a few trees, run bare `wt`, check preview/select/cancel);
everything around it — resolution, fallback, the cd protocol —
is covered by the test suite.

## Completions

The shim registers cobra-generated zsh completions by evaluating
`wt completion zsh` at shell startup,
so they always match the installed binary.
They activate only if `compinit` has run before the eval line.

## Prompt indicator

Opt in with:

```sh
eval "$(wt shell-init zsh --prompt)"
```

A `chpwd` hook exports `WT_PROMPT` with the tree's directory name
while the cwd is inside a linked worktree, and unsets it elsewhere.
The hook is pure zsh — file stats, one `read`,
and a per-directory cache; it never runs a subprocess,
so your prompt latency is untouched.

Use it anywhere zsh expands prompts:

```sh
setopt prompt_subst
PROMPT='%~${WT_PROMPT:+ (⌂ $WT_PROMPT)} %# '
```

### Starship

Prefer [starship](https://starship.rs)? Skip `--prompt` and add a
[custom command](https://starship.rs/config/#custom-commands)
segment instead:

```toml
[custom.wt]
command = "basename $PWD"
when = '[ -f .git ] && grep -q "gitdir:.*worktrees" .git'
symbol = "⌂ "
style = "bold blue"
```

Starship runs `when` per prompt render;
it is two stats and a grep, comfortably under its 500ms budget.
Unlike the `WT_PROMPT` hook it only fires at a tree's root —
subdirectories of a tree show no segment.
