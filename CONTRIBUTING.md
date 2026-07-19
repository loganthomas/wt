# Contributing to wt

## Branches and pull requests

- `main` is the release branch; `dev` is the integration branch.
- Branch off `dev` and open your PR against `dev`.
  `dev` reaches `main` only through a release PR,
  merged with a merge commit (never squashed),
  so the two branches never diverge.
- CI must be green to merge: tests, lint, and the news-fragment check.
- Enable the local pre-commit hook once per clone:
  `git config core.hooksPath .githooks`.

## News fragments (changelog entries)

Release notes are built from small "news fragment" files,
not from commit messages,
so they are written for end users and survive any merge strategy.
**Every PR into `dev` must add at least one fragment.**
CI enforces this;
the only exception is a release-batch PR,
which consumes fragments instead of adding them.

### Adding a fragment

A fragment is one file with one user-facing sentence:

```
.changes/<pr>.<type>.md
```

Create it with the helper
(flags for scripts and agents, prompts for humans):

```sh
# non-interactive
go run ./tools/changelog new -pr 17 -type enh "\`wt new\` refuses to reuse a checked-out branch."

# interactive: prompts for PR number, type, and message
go run ./tools/changelog new
```

Or write the file by hand; the tool only saves typing.
Describe the change **to end users**, one sentence,
ending with the PR reference the tool appends for you:

```
$ cat .changes/17.enh.md
`wt new` refuses to reuse a checked-out branch. (#17)
```

### Fragment types

| Type    | Changelog heading | Use for                                                 |
| ------- | ----------------- | ------------------------------------------------------- |
| `enh`   | Enhancements      | New commands, flags, and behavior improvements          |
| `bug`   | Fixes             | Anything a user could have hit                          |
| `dep`   | Deprecations      | Commands, flags, or config keys scheduled for removal   |
| `doc`   | Documentation     | User-facing docs (not code comments)                    |
| `maint` | Infrastructure    | CI, build, release, and tooling changes users never see |

### Checking pending notes

```sh
go run ./tools/changelog pending
```

## Release flow (maintainers)

1. Branch off `dev`, batch the staged fragments, and PR back into `dev`
   (the fragment check passes because fragments are consumed):

   ```sh
   go run ./tools/changelog batch v0.1.0-alpha.2
   git add -A && git commit -m "chore: collect release notes for v0.1.0-alpha.2"
   ```

2. PR `dev` into `main` and merge with a merge commit.
3. Tag `main` with the batched version and push the tag:

   ```sh
   git tag v0.1.0-alpha.2 && git push origin v0.1.0-alpha.2
   ```

   The release workflow extracts this version's section from
   `CHANGELOG.md` and publishes it as the GitHub release notes;
   tagging a version that was never batched fails the release job.
