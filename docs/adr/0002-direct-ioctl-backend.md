# ADR-0002: The I2C backend is a direct `I2C_RDWR` ioctl through `golang.org/x/sys`, not periph.io

## Status

## Context
vane needs one way to perform I2C transactions on `/dev/i2c-N` on a Pi 5. Two
candidates: periph.io (`periph.io/x/conn/v3` + `periph.io/x/host/v3`), the
established Go hardware library, or calling the kernel's `i2c-dev` ioctl
directly through `golang.org/x/sys/unix`.

Constraints from the requirements: one admitted dependency (RNF-03), no global
state or `init()` registration, a `context.Context` on every operation
(RNF-05), and an explicit choice of ioctl, because which ioctl is used decides
whether the kernel checks driver ownership at all (RF-07, R-03).

## Decision
`i2cdev.Bus` opens `/dev/i2c-N` with `O_RDWR|O_CLOEXEC` and performs every
transaction as one `I2C_RDWR` ioctl carrying one or two `i2c_msg` (write, then
read with a repeated start). `golang.org/x/sys/unix` is the only dependency and
`i2cdev` is the only package that imports it. `I2C_SLAVE` and `I2C_SLAVE_FORCE`
are never used.

## The alternative that was rejected
**periph.io.** It is the natural choice for Go on a Pi, it has I2C fakes
(`i2ctest`), and a consumer already using it would get a familiar type. It was
rejected because:

- `host/v3` brings drivers for many platforms and needs `host.Init()`, which
  populates a **global registry** — the pattern the requirements forbid.
- `i2c.Bus.Tx(addr, w, r)` takes **no `context`**; vane would check the context
  around it anyway, so periph adds a layer without adding cancellation.
- It **abstracts the ioctl**. Which ioctl runs matters here: in
  `raspberrypi/linux` `rpi-6.12.y`, `i2c-dev.c` checks ownership only for
  `I2C_SLAVE` (line 414), and `I2C_RDWR` checks nothing. vane must know exactly
  what reaches the kernel to reason about RF-07.
- Two modules instead of one, against RNF-03; and Pi 5 support in `host/v3`
  (last release Apr 2025) is unconfirmed — though through `/dev/i2c-N` the SoC
  matters little.

## Consequences
- vane owns ~100 lines of ioctl code and its errno mapping (`EREMOTEIO`/`ENXIO`
  → `ErrNotPresent`, `EACCES` → `ErrPermission`), which must be tested on the Pi
  because no fake exercises the real ioctl.
- Its own fakes (`vanetest`) were planned anyway; periph's would not have
  replaced the register models.
- `vane.Bus` is shaped like periph's `i2c.Bus` (`Tx(addr, w, r)` plus context),
  so an adapter either way is a few lines.
- `I2C_RDWR` carries the address per message, so the fd holds no per-address
  state and one `Bus` serves every chip.

## Reopening criterion
A consumer needs vane over a periph.io bus (or the reverse). Then add
`vane/periphbus` as a **separate module** so the core keeps one dependency.
