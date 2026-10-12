# Releasing

One maintainer cuts releases. These are the steps that are easy to get
backwards under pressure. The branch model is in
[`CONTRIBUTING.md`](CONTRIBUTING.md#git-flow).

## Cutting a release

**1. Release branch from the `develop` commit you verified.**

```bash
git switch develop && git pull --ff-only
git switch -c release/vX.Y.Z
```

Only release edits go here: rename `## [Unreleased]` in `CHANGELOG.md` to
`## [X.Y.Z] - YYYY-MM-DD`, open a fresh `[Unreleased]` above it, and fix what
verification finds. Features wait for the next release.

**2. Verify on that commit, not from memory.** CI on the release pull request,
plus the hardware suite on the Pi (`make hw-test`), with the board and overlay
noted in the pull request.

**3. Pull request into `main`, merged with a merge commit.**

```bash
gh pr create --base main --title "chore(release): vX.Y.Z" \
  --body "Verified on <sha>: <CI run>, hardware suite on <board, overlay>."
gh pr merge --merge
```

**4. Tag the merge commit on `main`, signed and annotated.**

```bash
git switch main && git pull --ff-only
git tag -a vX.Y.Z -m "vX.Y.Z — <one line on what it carries>"
git tag -v vX.Y.Z          # must print "Good \"git\" signature"
git push origin vX.Y.Z
```

`tag.gpgsign=true` with an SSH key signs annotated tags automatically. The
failure mode is forgetting `-a`: `git tag vX.Y.Z` makes a lightweight, unsigned
tag silently, and the release workflow rejects it. The key that signs must be
in [`.github/allowed_signers`](.github/allowed_signers).

**5. Merge back.** The release branch may hold commits `develop` lacks
(the CHANGELOG rename, release fixes):

```bash
gh pr create --base develop --head main \
  --title "chore(release): merge vX.Y.Z back into develop" --body "After vX.Y.Z."
gh pr merge --merge
```

## What the release workflow does

On the tag push, [`release.yml`](.github/workflows/release.yml) refuses to
publish unless: the tag is SemVer, points to a commit on `main`, is the newest
tag, is signed by an allowed key, `CHANGELOG.md` has its section, and build,
race tests, linux/arm64 cross-build, govulncheck, boundary and documentation
checks pass. It then publishes a source tarball (and `vane-probe` for
linux/arm64 once it exists) with `SHA256SUMS` and build provenance, release
notes made of the CHANGELOG section plus the generated API diff, and asks
proxy.golang.org for the version so pkg.go.dev shows it.

`vX.Y.Z-rc.N` tags go through the same gates and are marked pre-release.

## When it fails

**A tag is never moved or deleted** — the tag ruleset forbids it, and a
module version anyone has fetched is cached by the proxy forever. If the
workflow fails:

- **Before "Publish release"**: nothing was published. Fix on a
  `hotfix/vX.Y.Z+1` branch from `main` and release that version. Note the
  burnt version in the CHANGELOG.
- **After the GitHub release exists**: the release is real. Fix the notes by
  hand with `gh release edit`; never re-tag.

The workflow lands before v0.1.0 and has not been watched succeeding yet.
Consider `v0.1.0-rc.1` first: it exercises every gate on a version nobody
should depend on.

## Hotfix

```bash
git switch main && git pull --ff-only
git switch -c hotfix/vX.Y.Z+1
# fix, with a test seen failing first; CHANGELOG section for the version
gh pr create --base main --title "fix(<scope>): <what>"
```

Merge with a merge commit, tag as in step 4, merge back as in step 5.
