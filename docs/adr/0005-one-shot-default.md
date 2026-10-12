# ADR-0005: One-shot is the default acquisition mode; BDU is always on

## Status
Accepted — 2026-10-12

## Context
Both chips can run **continuously** at a fixed output rate, overwriting their
data registers, or in **one-shot** mode, converting once per trigger and then
idling. The choice decides how fresh a reading is, how much the sensor heats
itself, and what failure looks like.

The Pi 5 already heats the HAT (R-05); a continuously converting chip adds its
own heat to the temperature and humidity it reports (G-19). In continuous mode
a read can also return the same sample twice, or a sample the chip half
overwrote, without any error (G-05).

## Decision
- **One-shot by default** for both chips. Each `Read` triggers a conversion,
  waits for data-ready under the context, and reads the result. The sample is
  always new; the chip idles between reads.
- **Continuous is opt-in** (`WithMode(Continuous(rate))`). Reads return the
  latest sample and set `FlagStale` (no new data since the last read) and
  `FlagOverrun` (the chip overwrote an unread sample) from the status register.
- **BDU (block data update) is on in every configuration**, both modes. Without
  it, a multi-byte read can combine the high byte of one sample with the low
  byte of the next.
- **Reset detection follows the mode** (G-11). One-shot: a browned-out chip
  powers up with PD = 0, never raises data-ready, and the read ends in
  `ErrNotReady`; the next `Read` re-identifies and reconfigures. Continuous: the
  same reset shows as permanent `FlagStale`, which the consumer can see.
- `Close` writes power-down in either mode (RF-08).

## The alternative that was rejected
**Continuous by default**, as many sensor libraries do. Reads are cheaper (no
wait), but every reading carries self-heating and a possibly repeated sample,
and a chip reset produces no error at all — only a value that stops changing.
For a library whose consumer reports environment readings as events, a fresh
sample per read is the property worth paying latency for.

## Consequences
- Every one-shot read costs a conversion time, which grows with the internal
  averaging setting (R-11). Latency per setting is measured on the Pi and
  documented; `WithAveraging` trades it against noise.
- A consumer polling faster than the conversion time spends that time waiting.
  Such a consumer should choose continuous mode and read the flags.
- The register models must implement both modes and the status bits, so both
  paths are tested without hardware.

## Reopening criterion
Measured one-shot latency on the Pi 5 at the default averaging exceeds what
beacon's sampling interval allows.
