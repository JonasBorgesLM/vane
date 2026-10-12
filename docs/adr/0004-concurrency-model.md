# ADR-0004: A per-chip mutex covers trigger → wait → read; vane adds no bus-level lock and starts no goroutines

## Status
Accepted — 2026-10-12

## Context
vane is claimed safe for concurrent use (RNF-04). Two different atomicity needs
exist:

1. **A single transaction** (write register address, read data) must not be
   split by another transaction on the bus.
2. **A one-shot sequence** — write `ONE_SHOT`, poll status, read data — must not
   interleave with another sequence **on the same chip**, or one caller reads
   the sample another triggered, or a configuration write lands mid-conversion.

The v0.1 survey proposed a bus-level serialisation decorator
(`internal/busmw`) for (1).

## Decision
- **(1) is the kernel's job, and it already does it.** In `raspberrypi/linux`
  `rpi-6.12.y`, `i2c_transfer` takes the adapter lock around every transfer
  (`drivers/i2c/i2c-core-base.c:2335–2340`), and `I2C_RDWR` reaches it through
  `i2cdev_ioctl_rdwr`. `i2cdev.Bus` keeps a mutex only to protect its own fd
  against `Close` racing a `Tx`.
- **(2) is a `sync.Mutex` in each chip driver**, held from trigger to the end of
  the data read, and for configuration and `Close`. Different chips interleave
  freely between transactions.
- **No goroutines** inside vane in v0.1. `sensehat.Environment` reads its
  sources sequentially.
- **Across processes, nothing.** Another process on the same bus is a
  documented limitation (G-08); `flock` is deferred (RF-15, #48).

## The alternative that was rejected
**A bus-level mutex decorator** wrapping every `vane.Bus`. Rejected: it
duplicates the adapter lock for (1), and it cannot provide (2) — a lock held
per transaction still lets two sequences interleave between their transactions.
Making it hold across a sequence would serialise chips that do not need it and
move chip knowledge into the bus layer.

**One goroutine per chip in `Environment`.** Rejected for v0.1: both chips share
one adapter, which the kernel serialises, so parallel reads mostly wait on the
same lock; the cost is goroutine lifecycle, cancellation and leak testing.

## Consequences
- Correctness under `-race` depends on every driver taking its mutex on every
  path; the register models in `vanetest` detect an interleaved sequence, so a
  missed lock fails a test rather than producing a plausible stale reading.
- `Environment` latency is the sum of the chips' latencies. Measured in M5.
- Two vane processes, or vane and the Python library, can still collide. The
  kernel keeps each transaction intact; sequences across processes are not.

## Reopening criterion
`Environment` latency measured on the Pi matters to beacon (parallel reads), or
a real deployment runs a second process against the HAT (`flock`, RF-15).
