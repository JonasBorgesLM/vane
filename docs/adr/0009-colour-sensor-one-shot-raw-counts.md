# ADR-0009: The TCS3400 is read one integration at a time and reported as raw counts with their gain and integration time; vane does not compute lux or colour temperature

## Status
Proposed — 2026-10-16

## Context
The Sense HAT v2 carries an ams TCS3400 colour sensor (#52, G-17). From its
datasheet, **v2-00, 2023-Feb-09** (DS000411), and the read-only probe and the
short write test recorded in [`docs/hardware.md`](../hardware.md), section *The
TCS3400 on the board*:

1. **Four variants, two addresses, two IDs.** TCS34001/3 at 0x39, TCS34005/7 at
   0x29; the `ID` register (0x92) reads 0x90 for TCS34001/5 and 0x93 for
   TCS34003/7 (pp. 20, 29). This board carries a TCS34001 (0x39, 0x90); 0x29
   does not answer. The kernel declares no node for it.
2. **A third register-addressing scheme.** The registers sit at 0x80–0xFF and
   bit 7 is part of the address; a burst always increments, with no bit to set
   (board — the datasheet does not describe it). Neither of `regio`'s schemes
   fits: `New` would read 0x80 as "register 0x00, auto-increment", and both
   refuse registers above 0x7F.
3. **No one-shot, and no per-sample data-ready.** With `AEN` on, the chip
   integrates continuously; `AVALID` says a cycle completed *since `AEN` was
   asserted* and then stays set (p. 20). On the board, turning `AEN` off cleared
   it, and turning it on again left it clear until one cycle had completed.
4. **What the counts mean depends on the configuration.** Integration time is
   (256 − `ATIME`) × 2.78 ms and the gain 1×, 4×, 16× or 64× (pp. 15, 18); the
   maximum count is min(cycles × 1024, 65535). Turning counts into lux or a
   colour temperature needs coefficients for the optics in front of the sensor
   (ams publishes the method in application notes), and on the Sense HAT the
   sensor sits under the board: it read C = 120 at the highest gain in a lit
   room, 3215 with the LED matrix white.

## Decision
- **Package `chip/tcs3400`, one `Sensor` per address.** `DefaultAddress` is
  0x39; `WithAddress(0x29)` for the other variants. `New` reads `ID` before any
  write (RF-01) and accepts 0x90 or 0x93 (bits 7:2 = 100100); `Variant` reports
  which pair the VID bits name. Probing both addresses, as G-17 asks, belongs to
  whoever opens the board (`sensehat`, later); the driver offers `Identify` for a
  read-only probe, as the v0.1 drivers do.
- **`regio.NewByteAddressed`**, a third scheme fixed per device (ADR-0008's
  rule): the sub-address is the full 8-bit register, bursts send it as is, and
  registers below 0x80 are refused, because 0x12 read 0x24 on the board — outside
  the map, not an error. Invariant 4 is amended with it.
- **One integration per `Read`, as ADR-0005 does for the other chips.** `Read`
  writes `ENABLE = PON | AEN`, waits for `AVALID` under the context, reads
  C, R, G, B in one 8-byte burst (the shadow registers keep each pair whole,
  p. 21), and writes `ENABLE = 0`. The sample is always new and the chip sleeps
  between reads. A timeout is the integration time plus a margin; the missing
  `AVALID` is `vane.ErrNotReady`.
- **Raw counts, with what they depend on.** Each channel is a
  `vane.Measurement[vane.Counts]`, a new dimensionless unit; the `Sample` carries
  the `Gain` and `IntegrationTime` used, so a consumer can normalise
  (counts / (gain × time)) or apply its own lux coefficients. `ASAT` (analog
  saturation) and a count at the configuration's maximum (digital saturation)
  set `vane.FlagClamped` on the channels concerned.
- **Defaults: 178 ms (`ATIME` 0xC0) and 16× gain**, the datasheet's
  characterisation condition (p. 8); `WithIntegration(cycles)` and `WithGain`
  change them. A read takes about one integration time.
- **Not used:** the wait timer, interrupts and thresholds, the IR channel
  (`0xC0`), and `sensehat` integration — each waits for a consumer that needs it.

## The alternative that was rejected
**Continuous integration, `Read` returning the latest sample.** Cheaper per
read, and what most libraries do. Rejected because the chip cannot say whether
the sample is new: `AVALID` stays set after the first cycle, so a `Read` faster
than the integration time returns the same sample silently, with no
`FlagStale` to set — the failure ADR-0005 exists to prevent. It also keeps the
chip awake. A consumer that wants back-to-back samples calls `Read` in a loop
and pays one integration each.

**Lux and colour temperature in vane.** Every colour-sensor library offers
them. Rejected: they are derived quantities, which vane does not compute (the
same boundary as CPU-heat compensation and dew point), and the coefficients
depend on the glass and the board around the sensor, which vane cannot know.
Counts with their gain and time are the measurement; lux is an interpretation.

**Normalised counts as the value** (counts per ms at 1×). Hides the number the
chip produced and the saturation limit, which depends on the integration time.
Rejected for the raw count plus the configuration.

## Consequences
- A third block-read scheme exists; the model must refuse sub-addresses below
  0x80 so a driver that inherits the ST habit is caught.
- `Read` is slow by design: 178 ms by default, up to 712 ms at the longest
  integration. Its context bounds it.
- Two writes per `Read` (`ENABLE` on and off) besides the identity check at
  `New`.
- On this board, little ambient light reaches the sensor: useful readings need a
  high gain and a long integration, or light from the LED matrix. That is a fact
  of the HAT, recorded, not something vane corrects.

## Reopening criterion
- A consumer needs samples faster than one integration per call: continuous
  mode with the interrupt (`AINT`) or the wait timer as the freshness signal.
- A consumer needs the IR channel.
- Lux becomes a need in more than one consumer: a separate package outside the
  driver, with coefficients the consumer supplies.
