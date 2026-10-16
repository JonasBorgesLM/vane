# CLAUDE.md

Guidance for Claude Code in this repository. The general engineering rules are
in `~/.claude/CLAUDE.md`; this file carries only what is true of vane.

**Status: v0.1.1 released.** The root package, `i2cdev`, both drivers,
`sensehat`, `vanetest` and `cmd/vane-probe` exist, are tested and were checked
on a Pi 5; what remains open is on the issue tracker. `REQUIREMENTS.md` and
`docs/architecture.md` are the plan the code is checked against.

## Project Overview

vane is a Go library that reads the environmental sensors of the Raspberry Pi
Sense HAT — HTS221 (humidity, temperature) and LPS25H/LPS25HB (pressure,
temperature) — on a Raspberry Pi 5, linux/arm64, without cgo. First consumer:
the beacon edge agent, through its own `SensorReader` port.

**Not responsible for**, and each of these is a deliberate boundary:

- Compensating temperature for CPU heat, dew point, or any derived quantity.
- Retry, circuit breaking, caching — the consumer's job (bastion, cistern).
- Serialisation, transport, or any knowledge of beacon.
- IMU, colour sensor, LED matrix, joystick — not in v0.1.
- Logging. vane has no logger and must not import crier or OpenTelemetry.

## Architecture

Planned dependency direction (G-03 in the requirements — getting it wrong is an
import cycle on the first `go build`):

```
sensehat ──▶ chip/hts221, chip/lps25h ──▶ vane (leaf) ◀── i2cdev, vanetest
                         └──────▶ internal/*
```

- `vane` (root package) is a leaf: types, errors, `Bus`. It imports no subpackage.
- `golang.org/x/sys` is imported by `i2cdev` and nothing else.
- `vanetest` chip models are written from the datasheet and import nothing from
  `chip/*`. A model sharing the driver's register constants replicates the
  driver's mistakes and the tests pass on a wrong address.

## Commands

```bash
go build ./... && go vet ./... && go test -race ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...   # the target (RNF-01)
golangci-lint run
./.github/scripts/check-boundaries.sh   # x/sys only in i2cdev, vane is a leaf, no I2C_SLAVE_FORCE
./.github/scripts/check-docs.sh         # ADR index, requirement ids, relative links
```

CI (`.github/workflows/ci.yml`) runs all of these plus fuzzing, macOS/Windows
tests, govulncheck, the git-flow and commit checks; `CI OK` is the one required
check. Release is `.github/workflows/release.yml`, driven by `RELEASING.md`.

## Conventions

- English in code, comments, docs, ADRs, issues and commits.
- Git flow and commit format are in `CONTRIBUTING.md` and checked by CI:
  `<type>/<issue>-<name>` branches squash into `develop`; only `release/*` and
  `hotfix/*` reach `main`, by merge commit; tags are signed and never moved.
- Conventional Commits with this project's scopes; the body says why and cites
  the requirement or ADR (`Refs RF-01`).
- Requirement ids are `RF-`, `RNF-`, `RI-`. Cross-project citations are
  qualified (`beacon/...`, `crier/...`) — unqualified ones are checked against
  this project's numbering by `check-docs.sh`.
- ADRs are never rewritten: amend or supersede. CI enforces "may gain lines,
  never lose them".

## Testing

- Drivers are tested over `vanetest.FakeBus`, never only on hardware.
- Hardware tests are `//go:build hardware`, run manually on the Pi. **Never** on
  a self-hosted runner triggered by a fork PR: that runs a stranger's code on a
  machine with bus access.
- Every bug in the bus protocol ships with a new transcript in `testdata/`.

## Known Constraints

- The `rpi-sense` overlay loaded from the HAT EEPROM declares the LPS25H (0x5C)
  and LSM9DS1 for kernel IIO drivers. Whether a driver binds depends on the
  kernel build: on the Pi 5 image in `docs/hardware.md` (6.12.62) none does.
  `I2C_RDWR` does not check for a bound driver, so vane's sysfs check is the
  only guard (#15). `overlays/vane-sense.dts` disables the sensor nodes so no
  driver can bind (#16). ADR-0001 (Accepted) is how vane handles a bound driver.
- The Pi is reached as `ssh raspberrypi.local` (key auth, `~/.ssh/config`);
  `make hw-test HW_HOST=raspberrypi.local HW_EXPECT=custom` runs the suite.
  `/usr/sbin` is not on `PATH` over non-interactive SSH.
- Serialisation is per process only. Another process on the same bus is not
  coordinated with.

## Non-negotiable Invariants

1. **No write before identification.** Every driver reads WHO_AM_I before its
   first write; a mismatch writes nothing (RF-01).
2. **Never `I2C_SLAVE_FORCE`**, and never access a chip a kernel driver owns
   (RF-07). Fighting the kernel's driver for the same chip reconfigures it under
   both parties.
3. **BDU is on in every configuration path** (RF-06). Without it a multi-byte
   read can mix the high byte of one sample with the low byte of the next.
4. **Multi-byte reads on ST chips go through the one block-read helper** that
   sets the auto-increment bit. Without it the chip returns the same register n
   times and the values still look plausible.
5. **No panic in a normal path; no global state; no `init()` registration.**
