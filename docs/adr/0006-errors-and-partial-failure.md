# ADR-0006: Errors are sentinels plus typed wrappers; the facade reports failure per sensor and fails as a whole only when nothing worked

## Status
Accepted — 2026-10-12

## Context
The Sense HAT holds independent chips on one bus. One can be absent, owned by a
kernel driver, mis-calibrated or slow while the others work. The brief asked
for "error per sensor"; it did not say what `Open` does when a chip fails at
start-up (G-06), nor what a single `Environment` call returns when half of it
failed.

Consumers need two things at once: to branch on the kind of failure
programmatically (`errors.Is`/`errors.As`), and enough context in the message to
log it upstream, because vane does not log (RNF-10).

## Decision
**Error values (RNF-06).**
- Sentinels for each kind: `ErrNotPresent`, `ErrUnexpectedChip`,
  `ErrKernelOwned`, `ErrPermission`, `ErrCalibration`, `ErrNotReady`,
  `ErrClosed`, `ErrUnsupportedPlatform`.
- `*SensorError{Source, Op, Reg, Err}` wraps every chip-level failure with where
  and during what it happened; `Unwrap` exposes the cause.
- `*IdentityError` and `*KernelOwnedError` carry the data a fix needs (want/got,
  driver name) and satisfy `errors.Is` against their sentinel.
- No panic on any path reachable from the public API.

**`sensehat.Open`.** Opens every chip it can and records the rest in
`Sensors()`. It returns an error only when a chip named in `WithRequired`
failed, or when no chip opened at all (`errors.Join` of the causes). A degraded
device is a working device.

**`sensehat.Environment`.** Each `Reading[T]` carries its own `Err`; a reading
with `Err == nil` is valid regardless of the others. The call returns a
top-level error only when the context ended, the device is closed, or **every**
reading failed (`errors.Join`).

## The alternative that was rejected
**All-or-nothing**: any chip failure fails `Open` and `Environment`. Simple to
use and impossible to misuse, but a missing humidity sensor would hide a working
pressure sensor, and beacon — whose job is to report what it can — would have to
reimplement per-chip handling around vane.

**A results-plus-error-slice API** (`Environment() (Environment, []error)`).
Rejected: the error is separated from the reading it belongs to, and callers
index two parallel structures.

## Consequences
- A caller that ignores `Reading.Err` and uses `Value` reads a zero value from a
  failed sensor. Godoc and every `Example` check `Err` first; this cannot be
  enforced by the type system without making the happy path heavier.
- `Open` succeeding does not mean every chip works; `Sensors()` says which, and
  `WithRequired` exists for callers that need a specific one.
- `errors.Join` messages are multi-line; consumers that log on one line format
  them.

## Reopening criterion
A consumer needs a failure mode this model cannot express without parsing
messages.
