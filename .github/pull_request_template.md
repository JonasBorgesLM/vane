<!--
Title: a Conventional Commit, ≤ 72 characters. It becomes the subject on a
squash merge. Add `!` for an incompatible API change.
  feat(hts221): refuse to write before WHO_AM_I matches
Branch: <type>/<issue>-<name>, into develop (CONTRIBUTING.md).
-->

## What and why

<!-- The diff says what changed. Say why it needed to. -->

## Traceability

- Requirements: <!-- RF-01, RNF-05 -- or "none" with a reason -->
- Decisions: <!-- ADR-0003 -- or "none" -->
- Closes: <!-- #26 -->

## Correctness checklist

Delete the lines that do not apply; keep the ones that do.

- [ ] Every new protection has a test that was **seen failing** with the
      protection removed; the negative control is noted above the test.
- [ ] No write reaches the bus before the chip is identified (RF-01).
- [ ] BDU stays on in every configuration path (RF-06).
- [ ] Multi-byte reads go through the block-read helper (auto-increment).
- [ ] `go test -race ./...` passes; new shared state is under the mutex that owns it (RNF-04).
- [ ] Context is checked before each transaction and each poll iteration (RNF-05).
- [ ] A bus-protocol fix ships with a new transcript in `testdata/`.
- [ ] Hardware checked on the Pi with `make hw-test` — say which board and overlay.

## Documentation

- [ ] Exported identifiers document the contract (units, flags, errors), not the signature.
- [ ] A structural decision has an ADR, written **before** the code; existing ADRs amended, never rewritten.
- [ ] `CHANGELOG.md` `[Unreleased]` updated for a user-visible change.
- [ ] `./.github/scripts/check-docs.sh` and `./.github/scripts/check-boundaries.sh` pass locally.
