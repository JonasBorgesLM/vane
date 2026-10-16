# Changelog

Notable changes to vane. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). In
v0.x a minor version may break the API, and every such break is listed under
**Changed** with a ⚠️ marker.

The release workflow copies the section of the version being tagged into the
GitHub release, next to the API diff it generates, and refuses to publish a tag
with no section here.

## [Unreleased]

### Added

- `chip/lps25h`: `WithAveraging(Averaging{Pressure, Temperature})`, now that
  the datasheets settled the sample counts (#17) and the board the bit
  positions. A zero field keeps the chip's own value, since the LPS25H and the
  LPS25HB reset differently. One-shot latency goes from 40.4 ms at the
  LPS25HB's 512/64 to 7.1 ms at 8/8.

## [0.1.1] - 2026-10-16

### Fixed

- `chip/hts221`, `chip/lps25h`: a one-shot `Read` could return a sample an
  earlier user of the chip had left unread, as if it were new, and from then on
  every reading was one conversion behind (#91). Seen on a Pi 5: the first
  reading of the day was 12 °C below the next. `New` now drops a leftover
  sample and lets a `ONE_SHOT` latched while powered down finish before the
  first trigger. It reads two registers more and writes nothing new.

## [0.1.0] - 2026-10-14

**First usable version (milestones M1–M5).** Reads humidity, pressure and
both chips' temperatures from a Sense HAT on a Raspberry Pi 5, without cgo,
checked on the board. No code changes since `0.1.0-rc.1`, whose release
workflow ran clean.

### Added

- `vane`: unit types (`Celsius`, `Hectopascal`, `RelativeHumidity`),
  `Measurement` with raw count, `Source`, time and quality `Flag`s; the `Bus`
  interface; sentinel errors (`ErrNotPresent`, `ErrUnexpectedChip`,
  `ErrKernelOwned`, `ErrPermission`, `ErrCalibration`, `ErrNotReady`,
  `ErrClosed`) and typed wrappers (`SensorError`, `IdentityError`,
  `KernelOwnedError`).
- `chip/hts221`: identification before any write, factory calibration read and
  validated (`Calibration.Validate`), one-shot by default and continuous on
  request, averaging (`WithAveraging`), BDU always on, recovery after a chip
  reset, `Identify` for read-only diagnosis.
- `chip/lps25h`: the LPS25H/LPS25HB family, same guarantees, with the overrun
  status bits reported as `FlagOverrun`.
- `i2cdev`: the Linux backend, one `I2C_RDWR` per transaction, never
  `I2C_SLAVE_FORCE`; a permission failure names the group to join.
- `sensehat`: `Open` probes the board, checks kernel ownership through sysfs
  before touching a chip, and opens what it can; `Environment` reads every
  chip with failure per sensor; `Diagnose` is a read-only look at the board.
- `cmd/vane-probe`: lists the adapters, the HAT and each chip's state;
  `-require` turns an unusable chip into exit status 1.
- `vanetest`: `FakeBus` with fault injection, independent register models of
  both chips, `Recorder` and `ReplayBus` for transcripts, and `FakeBoard` with
  the `*sensehat.Device` method set for consumers.
- `overlays/vane-sense.dts`: disables the sensor nodes the HAT's EEPROM
  overlay declares, so no kernel driver can bind; LED matrix and joystick keep
  working.
- Transcripts recorded on a Pi 5 for both chips, replayed in every `go test`.
- A conformance suite run against the models in CI and on the board
  (`make hw-test`); one-shot latency per averaging setting in the README.
- `docs/hardware.md`: what the board was observed to do, with the evidence;
  ADR-0001 (register access, the sysfs check as the only guard, the overlay
  recommended), Accepted after the hardware recon.
- README: Pi setup guide and a `FakeBoard` quick start.

### Fixed

- `vanetest` models now latch a `ONE_SHOT` written while the chip is powered
  down and run it on power-up, as both chips were observed to do.

### Known limitations

- A kernel driver bound after `Open` is not detected, and `I2C_RDWR` does not
  stop vane reaching it (ADR-0001).
- Serialisation is per process: another process on the same chips is not
  coordinated with.
- No averaging option for the LPS25H while the datasheet's sample counts are in
  conflict between sources (#17).
- Checked on one board: Pi 5, Sense HAT v2, Raspberry Pi OS (Debian 13, kernel
  6.12). Other distributions are untested.

## [0.1.0-rc.1] - 2026-10-14

The candidate for 0.1.0, identical to it in code; its content is listed
under 0.1.0.

## [0.0.1] - 2026-10-12

**Foundations (milestone M0). No library code yet** — this version exists so
`main` holds a released state and the release pipeline runs end to end once
before code depends on it. The module contains only the `vane` package
documentation; there is nothing to import.

### Added

- `REQUIREMENTS.md` v0.2: 31 requirement ids (RF, RNF, RI), the review findings,
  risks, milestones, and why there is no threat model.
- `docs/architecture.md`: package graph with each dependency rule and its
  enforcement, public signatures, the five main flows, the transcript format.
- ADR-0002 to ADR-0007: direct `I2C_RDWR` backend over `x/sys`, identification
  before any write, per-chip locking, one-shot default, per-sensor errors,
  write allowlist enforced by test models. ADR-0001 waits for the hardware
  recon.
- Go module `github.com/JonasBorgesLM/vane` with a Go 1.26 floor.
- CI: build and race tests at the floor and on stable, macOS and Windows
  tests, cross-build for linux/arm64 without cgo, dependency and package
  boundary guards, fuzzing, lint, govulncheck, documentation and ADR checks,
  git-flow and Conventional Commit checks, API compatibility against the latest
  tag.
- Release workflow: signed-tag verification, source tarball with build
  provenance, API diff in the release notes.
- Process: CONTRIBUTING (git flow, commits, signed commits), RELEASING,
  SECURITY, pull request template, Dependabot.
