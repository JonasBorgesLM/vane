# vane

[![CI](https://github.com/JonasBorgesLM/vane/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/JonasBorgesLM/vane/actions/workflows/ci.yml)

Go driver for the environmental sensors of the Raspberry Pi **Sense HAT** —
**HTS221** (relative humidity, temperature) and **LPS25H/LPS25HB** (pressure,
temperature) — on a **Raspberry Pi 5**, `linux/arm64`, **without cgo**.

Part of a small ecosystem of Go projects (moat, bastion, cistern, crier, cairn,
sapper, warden, usher). The name comes from the weather vane on top of a tower:
the instrument that read the environment.

> **Status: pre-code.** The requirements, the decisions and the pipeline that
> will check the code are in place; the library is not. Nothing here is usable
> yet, and the API below is the plan, not a promise. Progress is on the
> [project board](https://github.com/users/JonasBorgesLM/projects/8).

## What it will do

```go
dev, err := sensehat.Open(ctx)               // probes the board, checks kernel ownership
if err != nil { /* no chip could be opened */ }
defer dev.Close()

env, err := dev.Environment(ctx)             // partial failure is per sensor
if env.Humidity.Err == nil {
    fmt.Println(env.Humidity.Value, "%RH")   // vane.RelativeHumidity, not a bare float
}
```

- **Typed units** — `vane.Celsius`, `vane.Hectopascal`, `vane.RelativeHumidity`;
  every measurement also carries the raw ADC count, its source (chip, bus,
  address), the acquisition time and quality flags.
- **Partial failure** — one chip failing does not hide the others' readings.
- **Identification before any write** — each chip's WHO_AM_I is checked first;
  a wrong chip receives nothing.
- **Respects the kernel** — on a standard Raspberry Pi OS the kernel already
  drives the LPS25H; vane detects that and refuses with an explanation instead
  of fighting the kernel's driver for the chip.
- **Testable without hardware** — `vanetest` offers a register-level fake bus
  for driver tests and a board fake for consumers.
- **One dependency** — `golang.org/x/sys`, in the Linux backend only. Everything
  else compiles and tests on any OS.

## What it will not do

- Compensate temperature for the Pi's CPU heat, or compute dew point or any
  other derived quantity. It reports each chip's temperature so the consumer can.
- Retry, cache, serialise or transport readings — that is the consumer's job.
- Log. Errors carry chip, address, register and operation instead.
- Drive the IMU, the colour sensor, the LED matrix or the joystick — not in v0.1.

## Requirements

Raspberry Pi 5 with a Sense HAT (v1 or v2), Raspberry Pi OS 64-bit, read/write
access to `/dev/i2c-1` (the `i2c` group — no root). Go 1.26 or newer to build.

How to configure the Pi will be documented here with the first release; it
depends on [ADR-0001](docs/adr/README.md#open-questions), still open.

## Documents

| | |
| --- | --- |
| [`REQUIREMENTS.md`](REQUIREMENTS.md) | What vane must do, with the ids cited everywhere else |
| [`docs/adr/`](docs/adr/README.md) | Decisions, and the ones still open |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Git flow, branch names, commit format |
| [`RELEASING.md`](RELEASING.md) | How a version is cut and verified |
| [`SECURITY.md`](SECURITY.md) | Reporting a vulnerability; verifying a release |
| [`CHANGELOG.md`](CHANGELOG.md) | What changed, per version |

## License

[MIT](LICENSE)
