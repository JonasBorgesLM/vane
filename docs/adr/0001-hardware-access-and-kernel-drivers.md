# ADR-0001: vane drives the chips by register only, refuses a chip a kernel driver owns, and ships an overlay that keeps the kernel off the sensors

## Status
Proposed — 2026-10-14

## Context
The Sense HAT's EEPROM makes the Pi firmware load `rpi-sense` (v1) or
`rpi-sense-v2` before `config.txt` is read. That overlay declares the LPS25H
(0x5C) and the LSM9DS1 (0x1C, 0x6A) for kernel IIO drivers and leaves the
HTS221 node disabled (G-01). When a driver binds, raw register access through
`/dev/i2c-1` competes with it: both parties reconfigure the same chip, and
neither sees an error.

The plan assumed the conflict was certain. The recon on a Pi 5 (M1,
[`docs/hardware.md`](../hardware.md), section *Observations from the board*) found
three facts that shape the decision:

1. **On the image tested (Debian 13, kernel 6.12.62+rpt-rpi-2712) nothing
   binds.** The nodes are declared, but the kernel ships no `st_pressure*` or
   `st_lsm9ds1*` module, so every sensor is free. This is a property of one
   kernel build, not a promise; a different image or a rebuilt kernel brings
   the conflict back.
2. **`I2C_RDWR` does not check for a bound driver** (R-03, #15). `I2C_SLAVE`
   returns `EBUSY` for an address a driver owns; `I2C_RDWR`, the only ioctl
   vane uses (ADR-0002), takes each message's address and checks nothing.
   Confirmed in `drivers/i2c/i2c-dev.c` of `rpi-6.12.y` and observed with the
   kernel's `hts221` driver bound to 0x5F: `I2C_RDWR` read WHO_AM_I, `I2C_SLAVE`
   got `EBUSY`. **The sysfs ownership check is vane's only protection.**
3. **The nodes can be disabled without losing the HAT** (#16). An overlay that
   merges into the EEPROM overlay's three sensor nodes and sets them
   `disabled`, loaded from `config.txt`, removes the I2C devices before any
   driver can bind, keeps `/proc/device-tree/hat`, and leaves `sensehat@46`
   (LED matrix, joystick) working. Verified after a real reboot.

## Decision
Option A of the requirements (§6.1), adjusted to what M1 found:

- **Register access only.** The drivers talk to the chips through `vane.Bus`;
  there is no IIO backend in v0.1.
- **Ownership is checked before a chip is touched, every time.** `sensehat`
  reads `/sys/bus/i2c/devices/<bus>-<addr>/driver` for each chip before its
  first transaction; a bound driver gives `ErrKernelOwned` naming the driver,
  and the chip receives nothing. This check is mandatory, not a convenience,
  because the kernel does not provide one (fact 2). It is already implemented
  (`internal/kernel.Owner`, `sensehat.Diagnose`) and exercised on the board.
- **`overlays/vane-sense.dts` is the supported way to keep the kernel off the
  sensors**, recommended for every deployment, and the procedure
  `vane-probe` and `ErrKernelOwned` point to. On a kernel that binds nothing
  it is not needed for vane to work; it makes that condition hold through
  kernel upgrades instead of by accident.
- **vane never unbinds a driver itself** (option E). Unbinding needs root,
  does not survive a reboot, and changes the system under an operator who did
  not ask. It stays a documented manual step for an experiment.
- **Never `I2C_SLAVE_FORCE`** (option D), enforced by `check-boundaries.sh`.

## The alternative that was rejected
**B, IIO through sysfs, for the chips the kernel can drive.** It is the only
option that works on a stock system with no change at all, and on a kernel
that ships `st_pressure_i2c` it would read the LPS25H without fighting
anyone. It loses for v0.1 for three reasons. The HTS221 has no active driver
under the stock overlay (its node is disabled), so B alone cannot read
humidity and becomes C, two code paths with different behaviour per chip, from
the first release. It gives up the controls the drivers exist for: one-shot
on demand, BDU, the calibration check (`Calibration.Validate`), the flags.
And on the image tested it buys nothing, because no driver binds. B stays the
v0.2 candidate (#47), behind the same `sensehat` API, for a consumer that
cannot touch `config.txt`.

**Requiring the overlay** (A as the requirements wrote it, "every Pi must edit
`config.txt` and reboot") was also considered. Making it a precondition would
refuse to run on a system where vane already works, for a risk the runtime
check already covers. It is recommended, not required.

## Consequences
- **Per deployment, recommended:** copy `vane-sense.dtbo` to
  `/boot/firmware/overlays/`, add one `dtoverlay=vane-sense` line to
  `config.txt`, reboot, and confirm `Loaded overlay 'vane-sense'` in
  `sudo vclog -m`. That cost falls on every Pi running beacon, not once.
- **The firmware's `config.txt` parser has a trap**: on the Pi 5 tested, a
  98-character comment made it skip the next line silently, with nothing in
  `vclog` even under `dtdebug=1`. The procedure says to keep lines short and to
  check `vclog`; a silent skip otherwise looks like success.
- **With the overlay installed, the kernel cannot drive the LSM9DS1 or the
  LPS25H** for anyone else on that Pi. A user of those IIO devices loses them.
  A process that reads the chips through `/dev/i2c-1` itself is not affected
  by the overlay; it is a second process on the same bus, which vane does not
  coordinate with in v0.1 (G-08).
- **Residual, not covered:** a driver bound *after* `Open` (a `modprobe`, a
  `new_device`, a runtime overlay) is not detected, and because `I2C_RDWR`
  checks nothing, vane would then write to a chip the kernel drives. The
  overlay closes this for the declared nodes; nothing closes it for an
  operator who binds a driver by hand.
- **Residual, not covered:** the sysfs check trusts sysfs. A container without
  `/sys` mounted, or with a different root, sees no driver and lets vane
  through; `WithRoot` exists for tests and containers and must point at the
  host's view.

## Reopening criterion
- A consumer must run where `config.txt` cannot be changed **and** the kernel
  binds the LPS25H: build B (#47).
- A Raspberry Pi OS kernel that ships `st_pressure_i2c` binds it on a stock
  install: re-measure, and decide whether the overlay becomes required.
- A field report of a driver bound after `Open`: add a re-check before writes,
  at the cost of one sysfs read per configuration path.
