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
| [`0001`](0001-hardware-access-and-kernel-drivers.md) | Register access only; a chip a kernel driver owns is refused; `overlays/vane-sense.dts` keeps the kernel off the sensors | Accepted | — |
| [`0002`](0002-direct-ioctl-backend.md) | The I2C backend is a direct `I2C_RDWR` ioctl through `x/sys`, not periph.io | Accepted | — |
| [`0003`](0003-chip-identification.md) | Chips are identified by WHO_AM_I before any write; LPS25H/HB are one family; the HAT EEPROM is information only | Accepted | — |
| [`0004`](0004-concurrency-model.md) | A per-chip mutex covers trigger → wait → read; no bus-level lock, no goroutines | Accepted | — |
| [`0005`](0005-one-shot-default.md) | One-shot is the default acquisition mode; BDU is always on | Accepted | — |
| [`0006`](0006-errors-and-partial-failure.md) | Sentinels plus typed wrappers; failure per sensor, whole-call failure only when nothing worked | Accepted | — |
| [`0007`](0007-write-allowlist-in-tests.md) | Writes restricted to datasheet-writable registers, enforced by the test models, not at runtime | Accepted | — |
| [`0008`](0008-imu-batch-reads-from-the-fifo.md) | The LSM9DS1 is read in batches drained from its FIFO by the caller, through one driver for both addresses; no goroutine streams it | Accepted | — |

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
| `0007` | A third-party driver is accepted, or a recorded transcript shows a write a model rejects |
| `0001` | A consumer cannot change `config.txt` and the kernel binds the LPS25H; a stock kernel ships `st_pressure_i2c`; a driver bound after `Open` is reported |
| `0008` | A consumer needs the magnetometer at FIFO rate; `ReadBatch` cannot reach a needed rate at either bus speed; a second consumer writes the same streaming loop |

## Open questions

Deferred deliberately, listed rather than left implicit — an undecided question
that looks decided is the one that gets implemented by accident.

Numbers are reserved; none of these may be assumed in code until its ADR is
Accepted.

| ADR | Question | Issue | Depends on the Pi |
| --- | --- | --- | --- |
| — | None open. ADR-0001 was the last, Accepted after M1 | — | — |
