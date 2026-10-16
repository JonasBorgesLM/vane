# ADR-0008: The LSM9DS1 is read in batches drained from its FIFO by the caller, through one driver for both of its addresses; vane starts no goroutine to stream it

## Status
Accepted — 2026-10-16

## Context
v0.3 adds the Sense HAT's IMU, the LSM9DS1 (#51). It is unlike the two chips
v0.1 drives, in ways the v0.1 design did not have to face (G-18). From the
datasheet, **DocID025715 Rev 3** (March 2015, copy at
`cdn.sparkfun.com/datasheets/IoT/LSM9DS1.pdf`, read on 2026-10-16):

1. **Rate.** Accelerometer and gyroscope run at up to 952 Hz (p. 20). There is
   no one-shot: the chip converts continuously at its ODR, and a reading is the
   latest sample or what the FIFO kept.
2. **A FIFO.** 32 slots of the gyroscope's and the accelerometer's three axes,
   with Bypass, FIFO, Continuous and two mixed modes, an unread-sample count
   (`FIFO_SRC.FSS`), a threshold flag and an overrun flag (`OVRN`) (pp. 23–24,
   56–57). Reading it is how a consumer gets every sample instead of the last.
3. **Two I2C devices, one chip.** Accelerometer and gyroscope answer at 0x6A
   (`WHO_AM_I` = 0x68, p. 45), the magnetometer at 0x1C (`WHO_AM_I_M` = 0x3D,
   p. 40). Each has its own control, status and output registers; either can
   fail alone.
4. **Two auto-increment schemes.** On the magnetometer the sub-address MSB
   enables auto-increment, as on the HTS221 and LPS25H. On the accelerometer and
   gyroscope it does not: `CTRL_REG8.IF_ADD_INC` (default 1) does, and the
   sub-address carries the 7-bit address only (p. 29; `CTRL_REG8`, p. 53).
   Invariant 4 of `CLAUDE.md` assumes the MSB scheme for every ST chip, and
   `internal/regio` sets 0x80 on every block read.
5. **BDU twice.** `CTRL_REG8.BDU` for accelerometer and gyroscope (p. 53) and
   `CTRL_REG5_M.BDU` for the magnetometer (p. 65), both default 0.
6. **Bus throughput.** One accelerometer-plus-gyroscope sample is 12 bytes. At
   952 Hz that is about 11.4 kB/s of payload, above what a 100 kHz I2C bus
   carries once addressing and acknowledgements are counted; the chip supports
   400 kHz (p. 16). The Pi's `i2c-1` runs at 100 kHz unless `config.txt` says
   otherwise, and the whole bus — HTS221, LPS25H, the HAT's microcontroller at
   0x46, the colour sensor — would run at the new speed.
7. **Kernel ownership** is already decided: ADR-0001's sysfs check applies to
   both addresses, and `overlays/vane-sense.dts` disables both nodes.

## Decision
- **Package `chip/lsm9ds1`, one `Sensor` for the chip**, holding one register
  accessor per address. `New` identifies both (0x68 at 0x6A, 0x3D at 0x1C)
  before any write, per address (RF-01); a magnetometer that fails to identify
  leaves the accelerometer and gyroscope usable and reports per-quantity errors
  (ADR-0006), as `sensehat` does per chip.
- **Reads are pulled by the caller**, two ways, no goroutine (ADR-0004):
  - `Read(ctx) (Sample, error)` returns the latest sample of each quantity,
    with `FlagStale` from the data-ready bits — the continuous-mode contract the
    v0.1 drivers already have.
  - `ReadBatch(ctx, dst []MotionSample) (n int, err error)` drains up to
    `len(dst)` accelerometer-plus-gyroscope samples from the FIFO in one burst
    per contiguous run, oldest first, and reports a FIFO overrun on the batch
    (`FlagOverrun` on the samples after the gap) instead of hiding it. The
    caller chooses the cadence: at 952 Hz a 32-slot FIFO must be drained at
    least every 33 ms.
  - Streaming as a channel fed by a goroutine is left to the consumer, who
    wraps `ReadBatch` in its own loop and owns its lifecycle.
- **The magnetometer is not in the FIFO** (the chip does not store it there) and
  is read with `Read` only.
- **Each block read states its auto-increment scheme.** The magnetometer goes
  through `regio`'s block read as today. The accelerometer and gyroscope set
  `IF_ADD_INC = 1` in `New`, read with the sub-address MSB **clear**, and a
  model that rejects a multi-byte read without `IF_ADD_INC` set guards it.
  Invariant 4 is amended in the same change: the one block-read helper per
  scheme, not per chip family.
- **BDU on in both blocks**, in every configuration path (RF-06 extended).
- **Units in the root package, practical as `Hectopascal` is**:
  `vane.StandardGravity` (g), `vane.DegreesPerSecond`, `vane.Gauss`, each with
  the sensitivity from Table 3 (p. 12) applied per full scale. A three-axis
  value is `Vector3[T]{X, Y, Z T}` in `chip/lsm9ds1`, carried in the existing
  `vane.Measurement`, so G-18's "without changing the measurement types" holds.
- **400 kHz is the deployment's choice, measured before it is documented.**
  vane does not set or assume a bus speed. Before v0.3 ships, the board
  measures the highest ODR `ReadBatch` sustains without overrun at 100 kHz and
  at 400 kHz (`dtparam=i2c_arm_baudrate=400000`), and whether the HTS221,
  LPS25H and the microcontroller at 0x46 still work at 400 kHz.
- **`sensehat` is unchanged in v0.3**: the facade gains the IMU in a later
  change, once a consumer shows what shape it needs. A consumer uses
  `chip/lsm9ds1` directly until then.

## The alternative that was rejected
**A push stream: `Stream(ctx) (<-chan MotionSample, error)` with a goroutine
draining the FIFO.** It is what an IMU API usually looks like and what a
consumer may end up building. Rejected for vane: it starts the first goroutine
in a library that has none (ADR-0004), which brings its lifecycle (who stops
it, what happens to a slow reader, where its errors go) into vane, and the
choices it would have to make — buffer size, drop-oldest or block, error
channel or terminal error — belong to the consumer, who knows its rate and its
tolerance for loss. `ReadBatch` makes the consumer's loop a few lines and
leaves those choices with it.

**`Read` only, no FIFO.** Simplest, and enough for orientation at tens of Hz.
Rejected as the whole API: a poll loop at 952 Hz is a syscall per sample and
still misses samples under scheduling jitter, which the FIFO exists to absorb.

**SI units (m/s², rad/s, µT).** Defensible, and what many libraries use.
Rejected for consistency with v0.1, which reports hPa and °C rather than Pa and
K, and because the chip's datasheet, full scales and sensitivities are in g,
dps and gauss: converting inside vane hides the number a reader can check
against Table 3. A consumer converts with a constant.

## Consequences
- A second auto-increment scheme exists, and so a second way to get a block read
  silently wrong (the chip returning one register n times). The model guard and
  a transcript recorded on the board are what catch it.
- `ReadBatch` puts a timing duty on the consumer: drained too slowly, the FIFO
  overruns. vane reports it; it cannot prevent it.
- Running at high ODR may require a `config.txt` change and a reboot on every
  Pi, on top of the `vane-sense` overlay, and it changes the speed of every chip
  on the bus. Until measured, it is a stated risk, not a requirement.
- The facade does not expose the IMU in v0.3; `beacon` or another consumer
  depending on it imports `chip/lsm9ds1`.

## Reopening criterion
- A consumer needs the magnetometer at the FIFO's rate, or synchronised with
  accelerometer and gyroscope samples, beyond what `Read` gives.
- The board shows that `ReadBatch` cannot reach a rate a consumer needs at
  either bus speed: then interrupt-driven reads (`INT1_A/G` on a GPIO) come
  back as a question, with their own ADR.
- A second consumer writes the same goroutine around `ReadBatch`: then a
  streaming helper earns a place, outside the driver.
