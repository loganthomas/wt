# Pool mode

Pool mode exists for monorepos where a cold worktree is unusable:
a fresh checkout means a ten-minute install
and a 750k-file `node_modules` before the first command runs.
Instead of creating and destroying trees,
`wt` keeps a fixed pool of pre-warmed slots —
`pool-1 … pool-N` inside your trees directory —
that are **claimed**, worked in, and **released** back, warm.

Ordinary repos don't need any of this;
the default mode's create/remove lifecycle is simpler.
Reach for a pool when tree creation is what hurts.
How caches are shared (and deliberately not shared) across trees,
with per-ecosystem setups, is covered in [recipes.md](recipes.md).

## Enabling

Answer "yes" to the pool question in `wt init`
(or pass `--pool-size N`).
There is no mode switch:
the presence of a `[pool]` table in `.git/wt.toml` *is* pool mode.

```toml
base      = "main"
trees_dir = "../acme.trees"
copy      = [".env"]

[hooks]
setup              = "pnpm install"       # once, when a slot is provisioned
refresh            = "pnpm install"       # on claim — but only when…
refresh_if_changed = ["pnpm-lock.yaml"]   # …these files' hash changed

[pool]
size = 4
```

`wt init` provisions every slot up front:
a detached worktree parked on the base,
your `copy` files ported in,
the setup hook run,
and the `refresh_if_changed` hash recorded.
That's the slow part, paid once.

## The loop

```sh
wt new PROJ-123        # claim a slot, branch there, cd (under the shim)
# …work, commit, push…
wt done                # guards, park the slot, delete the branch, free the lease
```

In pool mode `wt new` and `wt done` keep their meaning —
start work, finish work —
but land in a slot instead of creating or removing a tree.
A claim is fast because the slot is warm:

1. take the slot's lease (free slots first, then provably dead ones);
2. reset: forced detach onto the base, then `git clean -fd` —
   never `-x`, so gitignored caches like `node_modules` survive;
3. check out your branch (created off the base when new);
4. re-port the `copy` files;
5. run `hooks.refresh` **only if** the `refresh_if_changed` hash moved —
   the lockfile short-circuit that turns minutes into seconds.

`wt done` releases: the same safety guards as the default mode
(dirty tree, unpushed commits, orphaned detached commits),
then the slot parks back on the base and the branch is deleted.
The tree itself is never removed — warmth is the whole point.

Personal trees still work in a pool repo:
anything that isn't a `pool-N` slot follows the default lifecycle.

## Plumbing: `wt claim` and `wt release`

Scripts and agents get explicit spellings:

```sh
path=$(wt claim PROJ-123)   # slot path on stdout, chatter on stderr
wt release PROJ-123         # park the slot, keep the branch
```

`wt claim` also accepts an **existing** branch and checks it out;
`wt new` refuses one, exactly as in default mode.
`wt release` always keeps the branch —
its lifecycle belongs to your PR flow —
where `wt done` deletes it (unless `--keep-branch`).

## Sizing

```sh
wt pool ls          # slot, state, branch, holder
wt pool resize 6    # grow: provision + warm the new slots
wt pool resize 2    # shrink: refuses while a doomed slot is claimed
```

## Leases and crashes

A claim is recorded as a lease naming the claiming session:
PID, its start time, hostname, branch, claim time.
A lease is stale only when its process is **provably dead**
(the PID is gone, or reused by a different process) —
never by wall clock —
so long-running work is never reaped,
and a crashed session never wedges a slot:
the next claim reclaims it, loudly.
Deadness is only provable on the host that claimed:
a lease from another machine — or from before a hostname change —
reads as unverifiable and is never reaped;
`wt release pool-N` clears it.
`wt pool ls` shows stale leases as `stale`;
a wedged slot can always be freed by hand with `wt release pool-N`.
A slot the guards refuse to reset — stranded commits, say —
is skipped with a notice and the claim moves on to the next one.

## Safety

Two guards make resets structurally safe (PLAN.md D14):

- **Pattern guard** — only a path of the exact form
  `<trees_dir>/pool-N` (symlinks resolved) can ever be reset,
  released, or removed by pool machinery.
  The main checkout and personal trees don't match, ever —
  which also means any `pool-N` name inside the trees dir
  is pool property; don't hand-make worktrees there.
- **Orphan guard** — no reset proceeds while a detached HEAD
  holds commits nothing else can reach;
  wt tells you how to rescue them instead.
