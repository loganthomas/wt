# Caches, sharing, and per-ecosystem recipes

You have one repo and several worktrees.
Do they share the expensive stuff — downloaded packages,
build caches, `node_modules` — or does every tree pay full price?

This page explains the model in plain terms,
then gives a short recipe per ecosystem.

## The mental model: two kinds of cache

Everything expensive in a checkout falls into one of two buckets,
and they behave completely differently.

**1. Machine-wide stores — shared across trees automatically.**
These live in your home directory, outside any tree,
and are looked up by *content*, not by which directory asked.
Examples: npm's tarball cache (`~/.npm`),
pnpm's store, Go's module cache,
Bazel's repository cache and disk cache.
If tree1 already downloaded or built something,
tree2 gets a cache hit — the tool does this by itself,
and wt's only job is to stay out of the way.

**2. Per-tree working state — never shared, on purpose.**
Things like `node_modules/` and Bazel's `bazel-out/`
live *inside* a tree and belong to it alone.
It is tempting to symlink them between trees, and wt refuses to:
linked dependency trees break the tools that read them
(module resolution, path-keyed caches, file watchers).
So this bucket cannot be shared sideways —
it can only be **built cheaply** (from bucket 1)
or **kept warm and reused over time** (pool mode).

That second point answers the common question directly:
**"tree1 already exists — will its caches speed up spinning
up tree2?"**
For bucket 1, yes, automatically.
For bucket 2, no — tree2 builds its own copy
(fast, because bucket 1 feeds it),
or, in [pool mode](pool-mode.md), tree2 is a slot
that was warmed up in advance and never goes cold:
resets use `git clean -ffd`, never `-x`,
so gitignored caches survive every claim/release cycle,
and `hooks.refresh` re-runs only when your lockfile
actually changed.

## Recipes

### Go

Nothing to do.
The module cache and build cache are already machine-wide;
a new tree's first `go build` reuses both.

### pnpm

Nothing to do.
pnpm's content-addressed store means each tree's `node_modules`
is mostly hard links into one shared store —
N trees cost little more than one.

```toml
[hooks]
refresh            = "pnpm install"
refresh_if_changed = ["pnpm-lock.yaml"]
```

### npm / yarn

Each tree carries a full `node_modules`, so budget disk for N copies
(`wt status` will show per-tree sizes in a later phase).
The global cache makes installs cheap-ish;
pool mode makes them rare:

```toml
[hooks]
setup              = "npm ci"
refresh            = "npm ci"
refresh_if_changed = ["package-lock.json"]

[pool]
size = 4
```

With this, a claim reinstalls only when the lockfile moved.

### Bazel

Two caches, two behaviors:

- The **repository cache** and a **disk cache** are shared across
  every tree — but the disk cache is opt-in.
  Turning it on is the single highest-leverage line here,
  and it is what makes tree1's build genuinely accelerate tree2's:

  ```
  # ~/.bazelrc
  build --disk_cache=~/.cache/bazel-disk
  ```

- The **output base** (analysis cache, `bazel-out/`) is keyed by
  the tree's directory *path*, so it is per-tree by nature.
  Pool slots help in a quiet way: their paths are stable
  (`../acme.trees/slot-3` forever), so a slot keeps the same
  output base across claims and stays warm,
  where throwaway trees at branch-named paths start cold each time.

### `.env` files and other untracked secrets

Untracked files don't follow you into a new tree.
List them once and wt copies them into every tree and slot —
copies, not symlinks, so nothing resolves paths strangely:

```toml
copy = [".env", ".envrc"]
```

On a claim the copies are refreshed from the main checkout;
an edited copy is treated as your data and guarded on the way out.

### direnv

Combine `copy = [".envrc"]` with a setup hook that runs
`direnv allow`, or wrap your hooks in `direnv exec . <cmd>`
so they see the tree's environment.

### One dev server per tree: ports

Parallel trees mean parallel dev servers fighting over ports.
Derive the port from the slot number instead of hardcoding it —
in a copied `.envrc`:

```sh
# slot-3 → port 3003
slot="${PWD##*slot-}"
case "$PWD" in *slot-[0-9]*) export PORT=$((3000 + slot)) ;; esac
```

wt has no port machinery on purpose; the pattern is one line.
