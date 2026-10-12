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

Each row links its file, so the index is navigable and the
`check-docs.sh` cross-check can verify both directions.

## Reopening criteria on the record

Decisions carrying an explicit trigger, so "revisit later" is not a synonym for
never.

| ADR | Reopen when |
| --- | --- |
| `0002` | A consumer needs vane over a periph.io bus — then a separate `periphbus` module |

## Open questions

Deferred deliberately, listed rather than left implicit — an undecided question
that looks decided is the one that gets implemented by accident.

Numbers are reserved; none of these may be assumed in code until its ADR is
Accepted.

| ADR | Question | Issue | Depends on the Pi |
| --- | --- | --- | --- |
| `0001` | Hardware access when kernel drivers own the LPS25H and LSM9DS1 | #18 | yes — blocked by #14, #15, #16 |
| `0003` | Chip identification and board revision (LPS25H/LPS25HB family) | #5 | no |
| `0004` | Concurrency model | #6 | no |
| `0005` | Default acquisition mode | #7 | no |
| `0006` | Error model and partial failure | #8 | no |
| `0007` | Write allowlist enforced in tests, not at runtime | #9 | no |
