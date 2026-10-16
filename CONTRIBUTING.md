# Contributing to vane

Work is tracked on the [project board](https://github.com/users/JonasBorgesLM/projects/8).
Every issue cites the requirement and the ADR it closes, and has a checkable
"done when". Read [`CLAUDE.md`](CLAUDE.md) for the invariants that are defects
if violated.

## Git flow

**`develop` is where work lands. `main` holds released states only.**
`develop` is the default branch, so pull requests target it by default and
`Closes #N` closes the issue when the pull request merges.

```
feat/26-…  ──●──●
                  \ squash
develop    ──●─────●──●─────────────────●──────▶
                        \               ↑ merge back (main → develop)
release/v0.1.0           ●──●           │
                             \ merge    │
main       ───────────────────●─────────┴──────▶   tag v0.1.0 on the merge
```

| Branch | From | Into | Merge method | PR title |
| --- | --- | --- | --- | --- |
| `<type>/<issue>-<name>` | `develop` | `develop` | **squash** | the change, e.g. `feat(hts221): …` |
| `release/vX.Y.Z` | `develop` | `main` | **merge commit** | `chore(release): vX.Y.Z` |
| `hotfix/vX.Y.Z` | `main` | `main` | **merge commit** | `fix(<scope>): …` |
| `main` (merge back) | — | `develop` | **merge commit** | `chore(release): merge vX.Y.Z back into develop` |

The `git-flow` CI job rejects any other pair: nothing but `release/*` and
`hotfix/*` reaches `main`.

**Why squash into `develop`, and merge commits everywhere else.** A work branch
is one subject, so it becomes one commit on `develop` whose subject is the PR
title — checked by CI — and `git log develop` reads as a list of changes. A
release or hotfix is merged with a merge commit so `main` and `develop` keep a
shared history; squashing them would give `main` commits `develop` never had,
and the next merge back would conflict on lines nobody changed. Rebase merging
is disabled.

### Work branches

```
<type>/<issue>-<short-name>

feat/26-hts221-identity     fix/33-lps25h-sign-extension
docs/2-requirements         ci/10-pipeline       test/30-hts221-transcript
```

Types: `feat fix docs test refactor perf build ci chore`. One branch, one
subject.

### Stacked pull requests

When work depends on an unmerged branch, base the new branch on it so its pull
request shows only its own diff. **Retarget the dependent pull request to
`develop` before merging the lower one**: head branches are deleted on merge,
and deleting a pull request's base branch closes that pull request in a state
that can be neither reopened nor retargeted (`usher` lost one this way).

```bash
gh pr edit <upper> --base develop   # first; its diff briefly shows both
gh pr merge <lower> --squash
```

## Commits

[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/), checked
by CI on every commit and on the pull request title:

```
<type>(<scope>)!: <subject in the imperative, ≤ 72 chars, no period>

<body: WHY the change was needed — the diff already says what. Wrap at 72.
Name the alternative you did not take when there was one.>

Refs RF-01, ADR-0003
Closes #26
```

- **Scopes**: `vane hts221 lps25h lsm9ds1 sensehat i2cdev vanetest regio poll
  kernel probe adr docs deps ci release`. A package without a scope here gets one in
  the same pull request that creates it.
- **`!`** marks an incompatible API change. In v0.x it is allowed but never
  silent: the `api-compat` job fails an incompatible change without it, and a
  `BREAKING CHANGE:` footer says what callers must do.
- **Requirement ids** in commits, godoc and tests are this project's (`RF-01`).
  Cite another project's qualified: `beacon/RI-02`.
- One subject per commit. Stage files explicitly; do not `git add -A` a tree
  holding two subjects.

### Closing keywords

GitHub closes an issue when a merged commit message or pull request description
holds a closing keyword (`close`, `fix`, `resolve`, in any form) next to its
number, and it does not read negation. "Does not close #41" closes #41, which
happened on #74. Write `Refs #41`, or just "#41 stays open", and keep the
keyword for issues the change finishes.

### Signed commits

The `main` and `develop` rulesets require **verified** signatures on every
commit in a pull request — the squash or merge commit GitHub creates is signed
by GitHub, but the PR's own commits must be signed too, or the merge stays
blocked with every check green (`usher` and `bastion` never required this, so
it was first hit here, on #56). Sign with the SSH key already used for tags,
registered on GitHub as a **signing** key:

```bash
git config --local commit.gpgsign true   # gpg.format=ssh and user.signingkey are global
git log --show-signature -1              # or: the "Verified" badge on GitHub
```

A commit made before this was set is re-signed with `git commit --amend -S
--no-edit` (or `git rebase --exec 'git commit --amend -S --no-edit' develop`)
and force-pushed to the work branch — never to `develop` or `main`.

## Before pushing

```bash
go build ./... && go vet ./... && go test -race ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
golangci-lint run
gosec -tests -exclude-generated ./...     # the CI runs it on its own, tests included
./.github/scripts/check-boundaries.sh
./.github/scripts/check-docs.sh
```

### Developing on macOS or Windows

The Linux backend (`i2cdev`) and its tests sit behind a `//go:build linux` tag,
so on another system `gosec` and `golangci-lint` do not look at them, the tests
do not run, and a clean local run proves little about them. This is not
hypothetical: it hid findings in `i2cdev` until the pipeline would have found
them. Run the pipeline's Linux checks in a container before pushing:

```bash
./.github/scripts/ci-linux.sh    # needs docker; reads its tool versions from ci.yml
```

It runs as an ordinary user and not root, because the permission tests skip for
root. The first run installs the linters into `~/.cache/vane-ci` and takes
minutes; later runs take seconds.

### Suppressing a gosec finding

Use `// #nosec G115 -- the reason`, not `//nolint:gosec`. golangci-lint honours
both, but the CI also runs gosec on its own, over test files too, and that one
reads only `#nosec`; a `nolint` passes locally and fails the pipeline. Give the
reason on the line, and one `#nosec` covers only the line it is on or the one
below.

## Hardware tests

The hardware suite (`//go:build hardware`) runs only on a Pi with the Sense
HAT, by hand. **No self-hosted runner is registered for this repository**, and
if one ever is, it runs only on branches of this repository, never on a pull
request from a fork: that would execute a stranger's code on a machine with
write access to the I2C bus, including the HAT EEPROM's write-protect register.

## Releases

See [`RELEASING.md`](RELEASING.md).
