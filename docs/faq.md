# FAQ

The questions `wt doctor` is adjacent to.
When something feels off, run `wt doctor` first —
every check prints its symptom, cause, and the exact fix.

## Why does `wt done` refuse a locked tree?

`git worktree lock` is your own "leave this alone",
usually protecting a tree on removable or network storage.
wt honors it everywhere it is destructive:
`wt done` and `wt clean` refuse locked trees,
and `wt ls` shows the `locked` flag.
When the lock has served its purpose:

```sh
git worktree unlock <path>
```

## A tree is listed but its directory is gone

Someone deleted the directory without telling git
(`rm -rf`, a cloud-sync purge, a reinstalled machine).
git flags the leftover registration as *prunable*;
`wt ls` shows it, `wt doctor` fails on it, and

```sh
wt clean
```

prunes the registration and drops wt's recorded state for it in one pass
(`-n` previews first).

## Does wt work with submodules?

git supports worktrees in repositories with submodules,
but the combination has sharp edges
(each new tree starts with uninitialized submodules).
wt adds no machinery — it warns in `wt doctor` and stays out of the way.
A `hooks.setup` of

```toml
[hooks]
setup = "git submodule update --init --recursive"
```

initializes them in every new tree automatically.

## My git hooks stopped firing in new trees

A relative `core.hooksPath` (husky's `.husky`, for example)
resolves inside *each* worktree.
If that directory is tracked, every tree carries it — fine.
If it is untracked or gitignored, hooks silently vanish in new trees.
Either keep the hooks directory tracked,
or point the path somewhere absolute:

```sh
git config core.hooksPath /absolute/path/to/hooks
```

`wt doctor` warns when it spots a relative hooks path.

## How much disk do my trees use?

```sh
wt status
```

shows a size per tree
(and `wt status --json` carries `disk_kb` for scripts).
Sizes are measured with `du` in parallel and cached for up to an hour,
so repeated calls stay instant on huge trees.
In pool mode the pool size caps the number of copies by design;
see [recipes.md](recipes.md) for per-ecosystem ways
to share caches across trees.

## `wt status` says the base is stale — what now?

wt never fetches behind your back on read commands (D7):
`wt status` (and `wt ls`, once wt has a fetch on record) only
reports the age of the last fetch.

```sh
wt sync          # fetch + fast-forward the base
wt sync --all    # pool repos: also re-park idle slots onto the new tip
```

`wt new` and `wt claim` fetch opportunistically when the base has
gone stale, so day-to-day you rarely need to think about it.

## Why does `wt clean` skip a merged tree?

`wt clean` removes a merged tree only when the same guards
`wt done` runs are satisfied.
A skip names its reason — usually uncommitted changes.
Commit or discard them, then run `wt clean` again.
A tree parked exactly on the base tip is also left alone:
wt cannot tell a freshly created tree from a fast-forward-merged one,
so it only reaps branches strictly behind the base.

## Something is wedged and I do not know what

```sh
wt doctor
```

exits 0 when healthy and 3 when something needs fixing,
with the fix command printed next to each finding.
For pool repos, `wt pool ls` shows who holds every slot,
`wt clean` releases provably dead leases,
and `wt release <slot>` is the documented escape hatch
for a lease record that cannot be read.
