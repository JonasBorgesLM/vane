# Architecture Decision Records

Every structural decision is recorded here. **An ADR is never edited to reflect
a later change of mind.** It is amended in place with a section naming what
superseded which part, so the reasoning that was current at the time stays
readable.

CI enforces the mechanical form: an existing ADR may gain lines, never lose
them.

New records start from [`TEMPLATE.md`](TEMPLATE.md), which is deliberately not
named `0000-` so it does not read as a decision or trip the index check.

## Index

| ADR | Title | Status | Amended by |
| --- | --- | --- | --- |
| [`0002`](0002-direct-ioctl-backend.md) | The I2C backend is a direct `I2C_RDWR` ioctl through `x/sys`, not periph.io | Accepted | — |
| [`0003`](0003-chip-identification.md) | Chips are identified by WHO_AM_I before any write; LPS25H/HB are one family; the HAT EEPROM is information only | Accepted | — |
| [`0004`](0004-concurrency-model.md) | A per-chip mutex covers trigger → wait → read; no bus-level lock, no goroutines | Accepted | — |
| [`0005`](0005-one-shot-default.md) | One-shot is the default acquisition mode; BDU is always on | Accepted | — |
| [`0006`](0006-errors-and-partial-failure.md) | Sentinels plus typed wrappers; failure per sensor, whole-call failure only when nothing worked | Accepted | — |

Each row links its file, so the index is navigable and the
`check-docs.sh` cross-check can verify both directions.

## Reopening criteria on the record

Decisions carrying an explicit trigger, so "revisit later" is not a synonym for
never.

| ADR | Reopen when |
| --- | --- |
| `0002` | A consumer needs vane over a periph.io bus — then a separate `periphbus` module |
| `0003` | M1 or a later board shows a behavioural difference between LPS25H and LPS25HB, or a substitute chip behind the same WHO_AM_I |
| `0004` | Measured `Environment` latency matters to beacon, or a second process uses the HAT in a real deployment |
| `0005` | One-shot latency on the Pi 5 at default averaging exceeds beacon's sampling interval |
| `0006` | A consumer needs a failure mode it can only express by parsing messages |

## Open questions

Deferred deliberately, listed rather than left implicit — an undecided question
that looks decided is the one that gets implemented by accident.

Numbers are reserved; none of these may be assumed in code until its ADR is
Accepted.

| ADR | Question | Issue | Depends on the Pi |
| --- | --- | --- | --- |
| `0001` | Hardware access when kernel drivers own the LPS25H and LSM9DS1 | #18 | yes — blocked by #14, #15, #16 |
| `0007` | Write allowlist enforced in tests, not at runtime | #9 | no |
