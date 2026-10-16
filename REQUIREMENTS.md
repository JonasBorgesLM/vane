# Requirements — vane

> Go driver for the Raspberry Pi Sense HAT sensors, part of the medieval
> ecosystem (moat, bastion, cistern, crier, cairn, sapper, warden, usher).
> The name comes from the weather vane on top of the towers: the instrument
> that read the environment.

| Field | Value |
| --- | --- |
| Version | **0.2** — baseline for implementation |
| Status | Accepted as the v0.1 scope. ADR-0001…0007 still open ([index](docs/adr/README.md)); ADR-0001 waits for the hardware recon (M1) |
| Date | 2026-10-12 |
| First consumer | beacon (edge agent, `SensorReader` port) |
| Target | Raspberry Pi 5 (8 GB) + Sense HAT (v1 or v2), linux/arm64, no cgo |
| Module | `github.com/JonasBorgesLM/vane`, Go 1.26 floor |

This document is binding. Every `RF-`, `RNF-` and `RI-` identifier below is
cited from commit messages, ADRs, godoc and tests, and `check-docs.sh` fails a
citation that resolves to nothing.

> **A requirement without a test that fails when the protection is removed
> counts as unimplemented.**

Cross-project citations are qualified — `beacon/RI-02`, `bastion/ADR-0009` —
so they are not checked against this project's numbering. The review findings
(`G-nn`) and risks (`R-nn`) are not requirements; they are cited for the
reasoning behind one.

What changed from v0.1, and why, is in [§10](#10-changes-from-v01).

**Hardware fact status** used throughout:

- **Confirmed** — checked against a primary source (kernel code, official
  documentation, datasheet, reference code).
- **Probable** — consistent secondary sources, no primary source yet.
- **To confirm** — from memory or a single unofficial source; needs a datasheet
  or a test on the board.

---

## Contents

0. [Brief review: gaps and proposals](#0-brief-review-gaps-and-proposals)
1. [Requirements](#1-requirements)
2. [Architecture and packages](#2-architecture-and-packages)
3. [Public API](#3-public-api)
4. [Patterns applied](#4-patterns-applied)
5. [Test strategy](#5-test-strategy)
6. [Decisions](#6-decisions)
7. [Risks and what to confirm on the hardware](#7-risks-and-what-to-confirm-on-the-hardware)
8. [Milestones](#8-milestones)
9. [Why there is no threat model](#9-why-there-is-no-threat-model)
10. [Changes from v0.1](#10-changes-from-v01)
- [Appendix A — Hardware facts](#appendix-a--hardware-facts)
- [Appendix B — Sanity check on the Pi 5](#appendix-b--sanity-check-on-the-pi-5)
- [Appendix C — Sources](#appendix-c--sources)

---

## 0. Brief review: gaps and proposals

The original brief is well founded (partial failure, context, fakes, BDU, no
cgo). The review found one hardware premise that does not hold on a standard
install, and design gaps that become API rework if left undecided.

### 0.1 Critical findings

| ID | Gap | Why it matters | Resolution |
| --- | --- | --- | --- |
| **G-01** | **The kernel already owns part of the chips.** The official `rpi-sense` overlay (and `rpi-sense-v2`) instantiates IIO drivers for the LPS25H at 0x5C and the LSM9DS1 at 0x1C/0x6A. The HTS221 node (0x5F) exists but is disabled. The overlay is loaded automatically from the HAT EEPROM. | Raw `/dev/i2c-1` access to the LPS25H competes with a kernel driver configuring the same chip. At best `EBUSY`; at worst (depending on the ioctl) both parties reconfigure the chip under each other. The same problem returns, larger, with the IMU. | ADR-0001. Mandatory ownership detection via sysfs before touching a chip (`ErrKernelOwned`). Register backend with a documented custom overlay that removes the sensor nodes. IIO backend evaluated for v0.2. Never `I2C_SLAVE_FORCE`. |
| **G-02** | **WHO_AM_I does not distinguish LPS25H from LPS25HB.** Resellers list the LPS25HB on the v2; it answers `0xBD` like the LPS25H and is register-compatible. | "Fail if the chip is not the expected one" cannot detect the swap. Naming the driver only LPS25H hides the v2. | ADR-0003. One compatible `LPS25H/LPS25HB` family. Board revision from a second channel (HAT EEPROM, `/proc/device-tree/hat/`, to confirm) as information only. |
| **G-03** | **Latent import cycle.** If units, errors and the board facade share the root package and the drivers import those types, the facade cannot import the drivers. | Structural error on the first `go build`. | Root package `vane` is a leaf (types, errors, `Bus`); drivers in `chip/*`; facade in `sensehat`. Enforced by `check-boundaries.sh`. |
| **G-04** | **"Raw reading" is ambiguous** — "no thermal compensation" or "ADC counts". | Without ADC counts the conversion cannot be audited or recalibrated later. | Every measurement carries the converted value **and** the ADC count (`Raw`). vane never compensates for CPU heat. |
| **G-05** | **No timestamp or sample freshness.** | The beacon needs event time; in continuous mode reading the same sample twice is silent. | `Measurement.Time` taken after data-ready, injectable clock. `Stale` and `Overrun` flags from the status register. |
| **G-06** | **Partial failure in `Open` undefined.** | If the HTS221 does not answer at boot, does `Open` fail or come up degraded? | `Open` comes up with what it finds and reports what failed; `WithRequired(...)` makes chips mandatory. ADR-0006. |

### 0.2 Design gaps

| ID | Gap | Resolution |
| --- | --- | --- |
| G-07 | **Cancellation is not instantaneous.** An I2C ioctl in flight cannot be interrupted by a `context`. | Check the context before each transaction and during the data-ready wait. Document the granularity: one transaction plus one poll interval. |
| G-08 | **Serialisation is per process only.** Another process (Python library, `i2c-tools`) can talk to the same chip. | Documented limitation. Cross-process `flock` deferred (RF-15). |
| G-09 | **Target OS undefined.** The group owning `/dev/i2c-1` varies (`i2c` on Raspberry Pi OS; `dialout` reported on Ubuntu). One of the Pis runs Kali. | Raspberry Pi OS 64-bit official; other distros best-effort with the group difference documented. |
| G-10 | **Bus safety.** Membership of the I2C group grants access to the whole bus, including the microcontroller at 0x46, where one register controls the HAT EEPROM write protection. | ADR-0007: each driver declares what it writes, and `FakeBus` fails a test on any other write. **This catches vane's own driver bugs only; it does not restrict any other process in the group.** |
| G-11 | **Recovery after a chip reset** (brown-out, BOOT). Configuration is lost and readings go wrong without an error. | **One-shot:** a reset shows up as `ErrNotReady` (power-down bit back to 0, data-ready never arrives) → re-identify and re-initialise lazily on the next read. **Continuous:** it shows up as permanent `Stale`. Also re-verify after a transaction error. **Retry with backoff is the consumer's job** (bastion), not vane's. |
| G-12 | **Corrupt calibration divides by zero.** If `H0_T0_OUT == H1_T0_OUT` (or the temperature equivalent) the HTS221 interpolation explodes. Identical values caused by read errors have been reported. | `Calibration.Validate()` in `Open`, `ErrCalibration`. Fuzzed. |
| G-13 | **Multi-byte reads on ST chips need the auto-increment bit** in the sub-address (MSB, to confirm in the datasheet). A classic silent error. | One internal block-read helper; `FakeBus` rejects a multi-byte read without the bit. |
| G-14 | **Hardware tests in CI** on a self-hosted runner in a public repository run third-party PR code on the Pi. | Hardware tests manual/local only. No self-hosted runner; if ever added, internal branches only, never fork PRs (CONTRIBUTING.md). |
| G-15 | **Observability without dependencies.** vane cannot import crier or OpenTelemetry. | No logs. `vane.Bus` is public, so a consumer decorates it for per-transaction telemetry. Hooks deferred until a consumer needs what decoration cannot give. |
| G-16 | **Building outside Linux.** Development on macOS/Windows must run the tests on fakes. | Core compiles on any `GOOS`; `/dev/i2c` backend behind `//go:build linux` with a stub returning `ErrUnsupportedPlatform`. |
| G-17 | **Ambiguous colour sensor.** TCS3400 variants at 0x29 and 0x39; the official Python library probes both; one report of no sensor at 0x29 on a Pi 5; no overlay node. | Risk for v0.4: probe both addresses and check the ID register. |
| G-18 | **The v0.1 API must not close the door on the IMU.** High rate needs a FIFO, possibly 400 kHz, and the kernel owns the LSM9DS1 too. | v0.1 is pull-only. Streaming arrives later in its own package without changing the measurement types. |
| G-19 | **Self-heating.** Continuous mode keeps the chip active, adding to the CPU's heat. | One-shot by default; `Close` powers the chips down. ADR-0005. |
| G-20 | **"Fake the whole library" has two levels.** Driver tests need a register fake; consumers (beacon) need a board fake. | `vanetest` offers both: `FakeBus` with chip models, `FakeBoard` with scripted scenarios. |

### 0.3 What stays as the brief had it

Explicit units, source per reading, error per sensor, `context.Context`, BDU, a
bus interface for fakes, no panic, no root, no cgo, minimal dependencies,
layered tests, immutable ADRs, and the develop → main flow.

---

## 1. Requirements

Priority in MoSCoW: **M** must, **S** should, **C** could. v0.1 scope unless
stated.

### 1.1 Functional

| ID | Prio | Requirement | Acceptance |
| --- | --- | --- | --- |
| **RF-01** | M | Identify each chip at run time by its WHO_AM_I register **before any write**. | A different value returns `*vane.IdentityError` (want and got) and `errors.Is(err, vane.ErrUnexpectedChip)`. No write reaches the bus before identification — asserted on the transcript, and seen failing with the check moved after configuration. |
| **RF-02** | M | Read relative humidity (%RH) and temperature (°C) from the HTS221, applying the factory calibration read and validated in `Open`. | Conversion matches test vectors derived from the datasheet; degenerate calibration returns `ErrCalibration`. |
| **RF-03** | M | Read pressure (hPa) and temperature (°C) from the LPS25H/LPS25HB. | Conversion matches test vectors, negative raw values included; both chips of the family accepted. |
| **RF-04** | M | Every measurement carries the typed value, the raw ADC count, the source (chip, bus, address), the acquisition time and flags. | Fields present and filled in every measurement, fakes included. |
| **RF-05** | M | Aggregate environment reading with partial failure and a per-sensor error. | With one chip failing, the others return valid measurements; the error names the chip and the operation. |
| **RF-06** | M | Acquisition mode per chip: one-shot (default) or continuous at a configurable rate. **BDU always on.** | The register fake proves BDU = 1 on every configuration path. |
| **RF-07** | M | Detect a chip bound to a kernel driver and refuse access with a clear error. **Never `I2C_SLAVE_FORCE`.** | `errors.Is(err, vane.ErrKernelOwned)`; the message names the driver found in sysfs. `check-boundaries.sh` fails on `I2C_SLAVE_FORCE` in non-test code. |
| **RF-08** | M | Explicit lifecycle: `Open`, idempotent `Close`. `Close` powers the chips down. Use after `Close` returns `ErrClosed`. | Fake test checks the power-down write and the post-close error. |
| **RF-09** | M | Register-level bus fake with HTS221 and LPS25H models, fault injection and latency. The models are written from the datasheet, independent of the drivers' constants. | Drivers tested entirely without hardware; `check-boundaries.sh` fails if `vanetest` imports `chip/*`. |
| **RF-10** | M | Board fake (`FakeBoard`) with programmable scenarios for consumers. | beacon can test its adapter without hardware and without knowing registers (RI-01). |
| **RF-11** | S | Record and replay bus transactions in a readable text format. | Transcripts recorded on the Pi replay in CI; a sequence divergence fails the test. |
| **RF-12** | S | Quality flags: outside the operating range, humidity clamped to 0–100 %, repeated sample (`Stale`), overwritten (`Overrun`). | Each flag has its own test. Flags never become errors. |
| **RF-13** | S | Report the board revision from the HAT EEPROM when available — information, never an identification criterion. | `BoardInfo` filled on the Pi; empty without error when the path does not exist. |
| **RF-14** | S | `vane-probe` diagnostic tool replacing the manual `i2c-tools` check: lists buses, identifies chips, shows kernel ownership and board revision, says what to do. | Readable output; non-zero exit when a required chip is missing. |
| **RF-15** | C | Cross-process advisory lock (`flock`), off by default. **Deferred out of v0.1** (#48). Reopen when a second process on the same Pi talks to the HAT in a real deployment. | Two processes with the option on do not interleave sequences. |

### 1.2 Non-functional

| ID | Category | Requirement |
| --- | --- | --- |
| **RNF-01** | Build | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` works with no extra toolchain. CI cross-builds and compiles the tests for this target. |
| **RNF-02** | Portability | Packages that do not talk to the hardware compile and test on any `GOOS`. Only the Linux backend uses a build tag. CI tests on Linux, macOS and Windows. |
| **RNF-03** | Dependencies | Core with zero external dependencies. The one dependency allowed in v0.1 is `golang.org/x/sys`, imported only by `i2cdev`. Enforced by `check-boundaries.sh` through `go list`. |
| **RNF-04** | Concurrency | Safe for concurrent use. Each transaction is atomic on the bus; the trigger → wait → read sequence is atomic per chip; different chips may interleave between transactions. The whole suite passes with `-race`. |
| **RNF-05** | Cancellation | The context is checked before each transaction and at each wait iteration. Cancellation latency bounded by one transaction plus one poll interval, documented. |
| **RNF-06** | Errors | No panic on a normal path. Sentinel and typed errors, wrapped with `%w`, compatible with `errors.Is` and `errors.As`. Errors carry chip, address, register and operation. |
| **RNF-07** | Privileges | Works without root, with read/write permission on `/dev/i2c-N` only. vane never writes outside what its drivers declare (ADR-0007). |
| **RNF-08** | Performance | Target of zero allocations per read at the chip-driver level, checked by benchmark. One-shot latencies measured on the Pi and recorded; numeric targets set after M1, not before. |
| **RNF-09** | Go version | The lowest viable floor. **Decided: Go 1.26**, set by `golang.org/x/sys` v0.49.0 (`go 1.26.0`), and the oldest release still receiving fixes. CI tests the floor and the current stable. Raising the floor is a CHANGELOG entry. |
| **RNF-10** | Observability | No internal logs. Errors carry enough context to log upstream. Per-transaction telemetry comes from the consumer decorating `vane.Bus`; built-in hooks are deferred (#49) until a consumer needs what decoration cannot give. |
| **RNF-11** | Quality | golangci-lint, gosec, govulncheck, tests with `-race`, short fuzzing in CI, an `ExampleXxx` in every public package, API compatibility (gorelease) against the latest tag, an incompatible change declared with `!`. |
| **RNF-12** | Process | README and code comments in English; ADRs immutable (amend or supersede); CHANGELOG; SemVer; signed tags; git flow per CONTRIBUTING.md (work into `develop`, releases into `main`). |
| **RNF-13** | Platform | Official: current Raspberry Pi OS 64-bit on the Pi 5. Best-effort: other arm64 distros (Ubuntu, Kali), with the differences documented. |

### 1.3 Integration

| ID | Requirement |
| --- | --- |
| **RI-01** | beacon consumes vane through `*sensehat.Device`'s method set (`Environment`, `Sensors`, `Close`) behind its own `SensorReader` port, and tests against `vanetest.FakeBoard`, which has the same method set — asserted at compile time. vane exports no interface for beacon to implement. |
| **RI-02** | vane imports nothing from beacon, crier, bastion or OpenTelemetry. Serialisation (Avro), transport (gRPC, Kafka), retry and caching live in the consumer. Enforced by the external-module guard (RNF-03). |
| **RI-03** | vane reports the temperature of **each** chip that measures it, so the consumer can compensate for CPU heat (R-05). vane itself never compensates. |

### 1.4 Out of scope (v0.1)

- IMU (LSM9DS1), colour sensor (TCS3400), LED matrix, joystick.
- CPU-heat temperature compensation, dew point, any derived quantity.
- Retry, circuit breaking and caching (bastion, cistern).
- Serialisation, transport, any knowledge of beacon.
- Streaming and interrupt-driven reads.
- Cross-process locking (RF-15), built-in hooks, a runtime write-allowlist
  decorator — deferred with reopening triggers (§10).

---

## 2. Architecture and packages

The full architecture with signatures and flows is `docs/architecture.md`
(#3). This section is the summary the requirements depend on.

### 2.1 Layers

```
 beacon edge agent
   └─ SensorReader port (defined in beacon)
        └─ beacon adapter  ──────────────────► vane/sensehat.Device   (board facade)
                                                   │
                                     ┌─────────────┴──────────────┐
                                     ▼                            ▼
                             vane/chip/hts221             vane/chip/lps25h      (register drivers)
                                     └─────────────┬──────────────┘
                                                   ▼
                                     vane.Bus  (one-method interface)
                                                   ▲
                     ┌─────────────────────────────┼──────────────────────────┐
                vane/i2cdev                 vanetest.FakeBus           vanetest.ReplayBus
           (/dev/i2c-N, Linux)          (register models,           (recorded transcripts)
                                         write allowlist check)
```

Dependency rules — each enforced by `check-boundaries.sh`:

- The root package `vane` is a leaf: it imports no subpackage.
- Drivers import only `vane` and `internal/*`.
- `sensehat` imports the drivers, `vane`, `i2cdev` and `internal/*`; it is the
  only place that knows the board.
- `i2cdev` imports `vane` and `golang.org/x/sys/unix`; no other package imports
  `x/sys`.
- `vanetest` imports `vane` (and `sensehat` for `FakeBoard`). Its chip models
  **do not import the drivers**: they are written from the datasheet, so a
  driver mistake is not replicated in the test oracle.

### 2.2 Package tree

```
vane/                              module github.com/JonasBorgesLM/vane
├── doc.go, units.go, measurement.go, source.go, bus.go, errors.go   (package vane)
├── chip/
│   ├── hts221/                    driver, registers, calibration, conversion, fuzz
│   └── lps25h/                    LPS25H/LPS25HB family driver
├── sensehat/                      Device, Open, Environment, detection, BoardInfo, Diagnose
├── i2cdev/                        /dev/i2c-N backend via I2C_RDWR (//go:build linux + stub)
├── vanetest/                      FakeBus, chip models, faults, Recorder, ReplayBus, FakeBoard
├── internal/
│   ├── regio/                     register read/write, block read with auto-increment
│   ├── poll/                      context-aware data-ready wait
│   └── kernel/                    driver ownership via sysfs, HAT EEPROM reading
├── cmd/vane-probe/                diagnostic tool
├── overlays/                      custom overlay without the sensor nodes (ADR-0001)
├── testdata/transcripts/          transcripts recorded on the Pi
└── docs/
    ├── adr/                       0001-*.md …
    ├── architecture.md
    └── hardware.md                facts confirmed in M1
```

### 2.3 Modules

One module in v0.x. The core is dependency-free by construction, since `x/sys`
only enters `i2cdev`. A separate module is justified only for an adapter with a
heavy dependency (e.g. `vane/periphbus`).

### 2.4 Extension point for non-register backends

The facade talks to an internal "environment source" interface per chip, not to
concrete drivers. An IIO backend (values already converted by the kernel) can
arrive in v0.2 without changing the public API, even without going through
`vane.Bus`.

---

## 3. Public API

Signatures, no bodies. `docs/architecture.md` is authoritative once written;
this is what the requirements assume.

### 3.1 Package `vane`

```go
// Units as named types: the compiler forbids mixing hPa with °C.
type Celsius float64
type RelativeHumidity float64 // percent, 0–100
type Hectopascal float64

type Chip string
const (
    ChipHTS221 Chip = "HTS221"
    ChipLPS25H Chip = "LPS25H" // register-compatible family: LPS25H and LPS25HB
)

type Source struct {
    Chip    Chip
    Bus     string // e.g. "/dev/i2c-1"
    Address uint16
}

type Flag uint8
const (
    FlagClamped    Flag = 1 << iota // value clamped to the physical range (RH > 100)
    FlagOutOfRange                  // outside the datasheet operating range
    FlagStale                       // no new sample since the previous read
    FlagOverrun                     // the chip reported overwritten data
)
func (f Flag) Has(x Flag) bool

type Measurement[T ~float64] struct {
    Value  T
    Raw    int32     // ADC counts before conversion
    Source Source
    Time   time.Time // taken after data-ready
    Flags  Flag
}

// Bus is the only hardware seam. One method keeps fakes and decorators trivial.
type Bus interface {
    // Tx writes w then reads into r as one combined transaction (repeated start).
    Tx(ctx context.Context, addr uint16, w, r []byte) error
}

var (
    ErrNotPresent          error // no ACK at the address
    ErrUnexpectedChip      error // identity register mismatch
    ErrKernelOwned         error // a kernel driver is bound to the device
    ErrPermission          error // cannot open the bus device
    ErrCalibration         error // factory calibration is degenerate
    ErrNotReady            error // data-ready did not arrive in time
    ErrClosed              error
    ErrUnsupportedPlatform error
)

type SensorError struct { Source Source; Op string; Err error }       // Error(), Unwrap()
type IdentityError struct { Source Source; Register, Want, Got uint8 } // Is(ErrUnexpectedChip)
type KernelOwnedError struct { Source Source; Driver string }          // Is(ErrKernelOwned)
```

### 3.2 Package `chip/hts221` (and, analogously, `chip/lps25h`)

```go
type Sensor struct { /* unexported */ }

// New identifies the chip, reads and validates calibration, and applies
// configuration. It never writes before identification succeeds (RF-01).
func New(ctx context.Context, bus vane.Bus, opts ...Option) (*Sensor, error)

func (s *Sensor) Read(ctx context.Context) (Sample, error)
func (s *Sensor) Source() vane.Source
func (s *Sensor) Calibration() Calibration
func (s *Sensor) Close(ctx context.Context) error // power-down; idempotent

type Sample struct {
    Humidity    vane.Measurement[vane.RelativeHumidity]
    Temperature vane.Measurement[vane.Celsius]
}

type Calibration struct { /* reference points as read from the chip */ }
func (c Calibration) Validate() error // ErrCalibration on degenerate points

type Mode struct { /* OneShot or Continuous(rate) */ }
func OneShot() Mode
func Continuous(rate Rate) Mode

type Option func(*config)
func WithAddress(addr uint16) Option
func WithBusName(name string) Option  // fills Source.Bus
func WithMode(m Mode) Option
func WithAveraging(a Averaging) Option // internal averaging: latency for noise
func WithClock(now func() time.Time) Option
func WithPollInterval(d time.Duration) Option
```

`lps25h.Sample` replaces `Humidity` with `Pressure vane.Measurement[vane.Hectopascal]`
and adds `Variant()`, which reports H or HB when they can be told apart and
`Unknown` otherwise.

### 3.3 Package `sensehat` (board facade)

```go
type Device struct { /* unexported */ }

// Open probes the board, checks kernel ownership, and initialises the chips it
// finds. It succeeds with a subset unless a chip marked as required fails.
func Open(ctx context.Context, opts ...Option) (*Device, error)

func (d *Device) Environment(ctx context.Context) (Environment, error)
func (d *Device) Sensors() []SensorInfo // what was found, what failed, and why
func (d *Device) Board() BoardInfo      // from the HAT EEPROM when available
func (d *Device) Close() error          // io.Closer; bounded by WithCloseTimeout

type Environment struct {
    Humidity     Reading[vane.RelativeHumidity]
    Pressure     Reading[vane.Hectopascal]
    Temperatures []Reading[vane.Celsius] // one per chip that measures temperature (RI-03)
}

// Reading pairs a measurement with its own error; Err == nil means it is valid.
type Reading[T ~float64] struct {
    vane.Measurement[T]
    Err error // *vane.SensorError when not nil
}

// Environment returns a non-nil error only when the context ends, the device
// is closed, or every sensor failed (errors.Join of the per-sensor errors).

type SensorInfo struct { Source vane.Source; State State; Err error }
type BoardInfo struct { Product, Vendor, ProductVer string; Present bool }

type Option func(*config)
func WithBus(b vane.Bus) Option       // overrides path-based opening
func WithBusPath(path string) Option  // default "/dev/i2c-1", to confirm on the Pi 5
func WithChips(chips ...vane.Chip) Option
func WithRequired(chips ...vane.Chip) Option
func WithHTS221(opts ...hts221.Option) Option
func WithLPS25H(opts ...lps25h.Option) Option
func WithClock(now func() time.Time) Option
func WithKernelCheck(enabled bool) Option // default true; disabling is for tests only
func WithCloseTimeout(d time.Duration) Option

// Diagnose gathers what vane-probe prints, without initialising chips.
func Diagnose(ctx context.Context, opts ...Option) (Report, error)
```

### 3.4 Package `i2cdev` (Linux)

```go
type Bus struct { /* fd, path, mutex */ }
func Open(path string) (*Bus, error) // O_RDWR|O_CLOEXEC; ErrPermission on EACCES
func (b *Bus) Tx(ctx context.Context, addr uint16, w, r []byte) error // I2C_RDWR
func (b *Bus) Name() string
func (b *Bus) Close() error
```

On non-Linux platforms `Open` returns `ErrUnsupportedPlatform`.

### 3.5 Package `vanetest`

```go
type FakeBus struct{}                   // implements vane.Bus; routes by address
func NewFakeBus(devices ...Device) *FakeBus
func (b *FakeBus) Inject(addr uint16, f Fault) // NACK, short read, EIO, latency, stuck data-ready, reset
type Device interface { Addr() uint16; Tx(w, r []byte) error }

// Every write outside the device model's declared writable registers fails the
// test (ADR-0007), as does a multi-byte read without the auto-increment bit.

func NewHTS221(opts ...ModelOption) *HTS221Model // register file + factory calibration
func (m *HTS221Model) Set(rh vane.RelativeHumidity, t vane.Celsius)
func NewLPS25H(opts ...ModelOption) *LPS25HModel
func (m *LPS25HModel) Set(p vane.Hectopascal, t vane.Celsius)

func NewRecorder(inner vane.Bus, w io.Writer) *Recorder // records a transcript
func NewReplayBus(t testing.TB, transcript io.Reader) *ReplayBus

type FakeBoard struct{}                 // same Environment/Sensors/Close method set as *sensehat.Device
func NewFakeBoard(script ...Step) *FakeBoard
```

Transcript format (details in `docs/architecture.md`), one transaction per
line, readable in a diff:

```
# bus=/dev/i2c-1 board="Sense HAT" recorded=<date>
5f  W 0f            R bc
5f  W <reg|0x80>    R <n calibration bytes>
```

(The first line is the HTS221 WHO_AM_I read; the second, a block read.)

### 3.6 How beacon consumes it

beacon defines its own port (`SensorReader`) and an adapter translating
`sensehat.Environment` into its domain model. vane exports no interface for
beacon to implement ("accept interfaces, return structs"); `FakeBoard` has the
same method set as `*sensehat.Device`, so it satisfies beacon's port without
extra coupling (RI-01).

---

## 4. Patterns applied

| Pattern | Where | Why |
| --- | --- | --- |
| Ports and Adapters | `vane.Bus` as the port; `i2cdev`, `FakeBus`, `ReplayBus` as adapters | Isolate the hardware; test everything without it. |
| Decorator | `vanetest.Recorder`; consumer-side telemetry around `vane.Bus` | Cross-cutting behaviour without touching the drivers. |
| Facade | `sensehat.Device` | The consumer thinks in "environment", not chips and registers. |
| Functional options | `New`/`Open` everywhere | Ecosystem convention; evolve without breaking signatures. |
| Strategy | `Mode` (one-shot or continuous) per chip | Swap acquisition policy without scattered conditionals. |
| Value object | `Measurement`, `Calibration`, `Source` | Immutable once created; safe across goroutines. |
| Static probe table | Detection in `sensehat` | Declarative list of expected chips, addresses and identities. |
| Test double / record-replay | `vanetest` | Register fake for drivers, board fake for consumers, transcripts for fidelity. |
| Errors as values | Sentinels + types with `Unwrap`/`Is` | Programmatic handling and messages with context. |

Deliberately avoided: global state and `init()` driver registration (one reason
not to use periph.io's registry), internal goroutines in v0.1, large exported
interfaces, a bus-level middleware package (§10).

---

## 5. Test strategy

### 5.1 Layers

| Layer | Where | Covers |
| --- | --- | --- |
| Conversion (table) | CI, any OS | ADC → unit with vectors from the datasheet and ST technical notes; where none is published, vectors computed by hand and reviewed. Includes the HTS221 interpolation and LPS25H sign extension. |
| Fuzz | CI (short), local (long) | Conversions and `Calibration.Validate`: never panic, never NaN/Inf, degenerate calibration always rejected, interpolation monotonic for valid points. |
| Driver over the register fake | CI | Configuration sequence (BDU on, ONE_SHOT, power-down on Close), auto-increment bit, no write before WHO_AM_I, no write outside the declared set. |
| Fault injection | CI | NACK, short read, EIO, data-ready never arriving, wrong WHO_AM_I, corrupt calibration, chip reset mid-life. |
| Concurrency | CI with `-race` | Many goroutines on one `FakeBus`; the chip model detects illegally interleaved sequences. |
| Cancellation | CI | Context cancelled during the wait and between transactions, with simulated latency. |
| Facade | CI | Partial failure in `Open` and `Environment`; `WithRequired`; `ErrKernelOwned` with a simulated sysfs in a temp dir. |
| Transcripts (replay) | CI | Sequences recorded on the real Pi replayed byte for byte; any protocol change shows up as a diff. |
| Hardware | Pi only (`//go:build hardware`) | Identification, plausible readings, real latency, kernel ownership, behaviour with the default and the custom overlay. Bus configurable by environment variable. CI only compiles it. |
| Benchmarks | CI (fake) and Pi (real) | On the fake: library overhead and allocations. On the Pi: one-shot and continuous latency. |
| Examples | CI | Executable `ExampleXxx` against the fake, as living documentation. |
| Build matrix | CI | `linux/arm64` without cgo, `linux/amd64`, `darwin`, `windows`; Go floor and stable. |
| API | CI | gorelease against the latest tag; incompatible changes need `!`. |

### 5.2 Rules

- Hardware tests never run on a self-hosted runner triggered by a fork PR.
- The `vanetest` chip models are written from the datasheet and import nothing
  from `chip/*` (enforced).
- Every bus-protocol bug fix comes with a new transcript in `testdata/`.
- Every test guarding a requirement is seen failing with the protection
  removed; the negative control is noted above the test.

---

## 6. Decisions

Seven ADRs, all **open** until written and accepted
([index](docs/adr/README.md)). Decisions that had no alternative worth arguing
are recorded in `docs/architecture.md` instead of an ADR (§6.2).

### 6.1 ADRs

**ADR-0001 — Hardware access in the presence of kernel drivers** (#18, needs M1).
The official overlay binds IIO drivers to the LPS25H and LSM9DS1 and leaves the
HTS221 disabled (G-01).

| Option | For | Against |
| --- | --- | --- |
| A. Registers only, requiring a custom overlay without the sensor nodes | Full control (BDU, one-shot, calibration); a real driver; testable by register | **Every Pi running beacon must edit `config.txt` and reboot**; conflicts with users of the official library on the same system |
| B. IIO only, via sysfs | Works on a standard install; no special group | HTS221 has no active driver; less control; tests become a sysfs fake |
| C. Hybrid per chip | No configuration | Two code and test paths from day one; different behaviour per chip |
| D. Force access (`I2C_SLAVE_FORCE`) | No configuration | Fights the kernel driver for the same chip; **forbidden** |
| E. Unbind the kernel driver at run time (`/sys/bus/i2c/drivers/<drv>/unbind`) | No reboot; good for the M1 experiment | Needs root; not persistent; surprising for an operator |

Recommendation: A for v0.1, with mandatory ownership detection and an
explanatory error, and the custom overlay versioned in `overlays/`; E as the
first M1 experiment; B possible in v0.2 through §2.4 without breaking the API.
**Residuals to record:** a driver bound after `Open` is not detected; if `I2C_RDWR`
does not check ownership (R-03), the sysfs check is the only protection.
To confirm: how to suppress the EEPROM-loaded overlay and load the custom one on
the Pi 5 (`dtoverlay=` empty or `force_eeprom_read=0` are candidates).

**ADR-0002 — I2C backend: direct ioctl via `x/sys/unix` instead of periph.io** (#4).

| Criterion | periph.io (`conn/v3` + `host/v3`) | direct ioctl (`x/sys/unix`) |
| --- | --- | --- |
| Dependencies | Two modules; `host` brings drivers for many platforms | `x/sys` only |
| Activity | `conn` v3.7.3 (Apr 2026); `host` v3.8.5 (Apr 2025) | maintained by the Go team |
| Pi 5 | Pi 5 support in `host` unconfirmed; via `/dev/i2c-N` the SoC matters less | Indifferent |
| Context | `i2c.Bus.Tx(addr, w, r)` has no `context` | vane checks the context between transactions |
| Initialisation | `host.Init()` with a global driver registry | No global state |
| Testing | Has fakes (`i2ctest`) | Own fakes (already planned) |
| Fine control | Abstracts the ioctl | Explicit `I2C_RDWR` and ownership check |

Recommendation: direct ioctl. `vane.Bus` is shaped like periph's `i2c.Bus`, so
an optional adapter (`vane/periphbus`, separate module) is trivial if a consumer
asks — that is the reopening trigger.

**ADR-0003 — Chip identification and board revision** (#5). WHO_AM_I is
mandatory and gates every write. LPS25H and LPS25HB are one compatible family.
The HAT EEPROM is informative, never a criterion. A mismatch yields
`IdentityError` with want and got.

**ADR-0004 — Concurrency model** (#6). A per-chip mutex covers trigger → wait →
read. No bus-level mutex in vane beyond `i2cdev.Bus`'s own: the kernel already
serialises each `I2C_RDWR` on the adapter (cite the `i2c-core` lock in the ADR).
No internal goroutines in v0.1. Cross-process locking out of scope (RF-15).

**ADR-0005 — One-shot as the default acquisition mode** (#7). Fresh samples and
less self-heating; continuous optional with `Stale`/`Overrun`; BDU always on.
Reset detection per mode as in G-11.

**ADR-0006 — Error model and partial failure** (#8). Sentinels + typed errors
with `Unwrap`/`Is`; `Reading[T]` carries its own error in the facade; a
top-level error only when the context ends, the device is closed, or every
sensor failed (`errors.Join`). `Open` succeeds with a subset unless
`WithRequired` names a failing chip.

**ADR-0007 — Write allowlist enforced in tests, not at runtime** (#9). Each
driver declares the addresses and registers it writes; `FakeBus` fails a test on
any other write. Rejected: a runtime decorator — same protection against the
same bugs, one more layer in the hot path. **Residual:** no protection against
other processes in the i2c group. Reopen when a third-party driver is accepted
or a production write outside the set is observed.

### 6.2 Recorded in `docs/architecture.md`, not as ADRs

| Topic | Decision | Why not an ADR |
| --- | --- | --- |
| Module layout | One module in v0.x; split only for a heavy-dependency adapter | No argued alternative today |
| Units and measurement | Named `float64` types and generic `Measurement[T]`, not `periph.io/x/conn/v3/physic` | Follows from RNF-03 |
| Go floor | Go 1.26, set by `x/sys` (RNF-09) | A fact of the dependency, not a choice between options |
| Transcript format | Text, one transaction per line, hex, metadata header | Format detail; readable in diffs is the only criterion |
| Hooks | Deferred; consumers decorate `vane.Bus` (RNF-10) | Nothing to decide until a consumer asks (#49) |

---

## 7. Risks and what to confirm on the hardware

### 7.1 Risks

| ID | Risk | Prob. | Impact | Mitigation |
| --- | --- | --- | --- | --- |
| R-01 | Kernel owns the LPS25H on a standard install (G-01) | High | High | ADR-0001; sysfs detection; custom overlay; `vane-probe` explains what to do. |
| R-02 | The EEPROM overlay cannot be suppressed/replaced cleanly on the Pi 5 | Medium | High | Validate in M1; plan B is the IIO backend (v0.2 brought forward). |
| R-03 | `I2C_RDWR` does not check driver ownership and reaches a kernel-owned chip silently | **High** | High | From memory: `i2c-dev` checks busy addresses on `I2C_SLAVE`, not on `I2C_RDWR` — to confirm in M1 (#15). Sysfs check mandatory before `Open`. |
| R-04 | Board variant differs (LPS25H vs HB, future revisions with substitute chips) | Medium | Medium | Compatible family; EEPROM informative; transcripts per board. |
| R-05 | Pi 5 CPU heat distorts temperature and humidity | High | Medium for beacon | Out of vane's scope; documented; per-chip temperature exposed (RI-03). |
| R-06 | Bus name other than `/dev/i2c-1` on some kernel/distro | Low | Medium | `WithBusPath`; `vane-probe` lists buses. |
| R-07 | Access group differs per distro | Medium | Low | Per-distro docs; `ErrPermission` with a hint. |
| R-08 | Another process accessing the same chips | Medium | Medium | Documented; `flock` deferred (RF-15). |
| R-09 | `x/sys`'s Go floor higher than wanted | — | — | **Resolved:** floor 1.26 accepted (RNF-09). |
| R-10 | Name clash on pkg.go.dev/GitHub | — | — | **Resolved:** module path `github.com/JonasBorgesLM/vane`. |
| R-11 | One-shot with high internal averaging makes reads slow | Medium | Medium | `WithAveraging`; measure on the Pi; document latency per setting. |
| R-12 | Colour sensor at 0x29 or 0x39, reported problem on the Pi 5 | Medium | Medium (v0.4) | Probe both addresses with the ID register when v0.4 starts. |

### 7.2 M1 checklist (on the board)

- [ ] Sense HAT revision (v1/v2) and contents of `/proc/device-tree/hat/`.
- [ ] Real header bus (`i2cdetect -l`) and addresses present (`i2cdetect -y 1`).
- [ ] Which addresses show as `UU` and which driver is bound to each (sysfs).
- [ ] HTS221 WHO_AM_I (free) and, with the custom overlay, the LPS25H/HB's.
- [ ] Procedure to suppress the EEPROM overlay and load the custom one.
- [ ] Whether `I2C_RDWR` refuses an address with a bound driver.
- [ ] Group and permissions of `/dev/i2c-1` on the chosen OS.
- [ ] Kernel (`uname -r`) and Raspberry Pi OS versions.
- [ ] From the datasheet: auto-increment bit, HTS221 BDU/ONE_SHOT/PD bits,
      LPS25H temperature formula, conversion times per averaging setting,
      LPS25H vs LPS25HB differences.

---

## 8. Milestones

Each milestone ends with pull requests into `develop`, `CI OK` green, and the
"done when" of each issue checked. The board is
[project 8](https://github.com/users/JonasBorgesLM/projects/8).

| Milestone | Scope | Done when | Needs the Pi |
| --- | --- | --- | --- |
| **M0 — Foundations** | Kit, this document, `docs/architecture.md`, ADR-0002…0007, CI with guards, repository settings | ADRs 0002–0007 Accepted; every CI guard seen failing | no |
| **M1 — Hardware recon** | Appendix B on the Pi, `docs/hardware.md`, `I2C_RDWR` vs kernel drivers, custom overlay in a real boot, datasheet facts, ADR-0001 | Every Appendix A row Confirmed or turned into an explicit risk; ADR-0001 Accepted | **yes** |
| **M2 — Core & fakes** | Package `vane`, `internal/regio`, `internal/poll`, `FakeBus` with faults, Recorder/ReplayBus | `ExampleXxx` in public packages; each fault and assertion seen firing | no |
| **M3 — HTS221** | Driver, calibration with `Validate`, conversion, modes, independent model, fuzz, first transcript | Vectors pass; fuzz clean; real transcript replays in CI; no write before WHO_AM_I proven | transcript only |
| **M4 — LPS25H/HB** | Family driver, conversion, modes, status flags, model, transcript with the custom overlay | As M3; `Variant()` documents what can and cannot be told apart | transcript only |
| **M5 — Linux backend, facade, probe** | `i2cdev`, `internal/kernel`, `sensehat`, `FakeBoard`, `vane-probe`, hardware suite, benchmarks | Hardware suite passes with the custom overlay; `ErrKernelOwned` with the default one; zero allocations per read proven | suite only |
| **M6 — v0.1.0** | README with the Pi setup guide, API gate, CHANGELOG, signed tag, beacon spike | pkg.go.dev renders with examples; beacon tests against the published tag | — |

**M1 and M2 run in parallel.** Only ADR-0001, the transcripts and the hardware
suite need the Pi.

### After v0.1

| Version | Scope | Depends on |
| --- | --- | --- |
| v0.2 (optional) | IIO backend for kernel-owned chips (#47) | M1 result and real use in beacon |
| v0.3 | LSM9DS1 IMU: FIFO, high-rate reads, streaming, 400 kHz (#51) | New ADRs on streaming and IMU kernel ownership |
| v0.4 | TCS3400: 0x29/0x39 probing and ID register (#52) | Confirmation on a v2 board on a Pi 5 |
| future | LED matrix and joystick | beacon needing alert commands |

---

## 9. Why there is no threat model

vane holds no credentials, money or personal data and acts on nobody's behalf.
Its security-relevant surface is narrow and covered by requirements instead:
never forcing a kernel-owned address (RF-07), writing only declared registers
(RNF-07, ADR-0007 — whose residual, other processes in the i2c group, is stated
there), surviving a malicious or faulty device's responses (RNF-06, fuzzing),
and not running untrusted code on the Pi (G-14). `SECURITY.md` states what is in
and out of scope for reports. Revisit if vane ever gains a network surface or
writes persistent state on the device.

---

## 10. Changes from v0.1

v0.1 was the Portuguese survey reviewed before the backlog was created. v0.2
translates it and applies what that review decided:

| Change | Why |
| --- | --- |
| `internal/busmw` removed. No bus-level serialisation decorator. | The kernel serialises each `I2C_RDWR` on the adapter and `i2cdev.Bus` holds a mutex; the atomicity that matters is per chip (ADR-0004). |
| Built-in hooks deferred (RNF-10 amended; old ADR-0011 dropped). | `vane.Bus` is public, so a consumer can decorate it. Reopen trigger: a consumer needs what decoration cannot give (#49). |
| Write allowlist moved from a runtime decorator into `FakeBus` (ADR-0007). Its stated motive corrected. | It catches vane's own driver bugs; it never protected against other processes in the i2c group, which the v0.1 text implied. |
| RF-15 (`flock`) deferred out of v0.1. | No second process exists in any planned deployment yet (#48). |
| 12 ADRs reduced to 7; module layout, unit types, Go floor, transcript format and hooks moved to `docs/architecture.md`. | An ADR is for a contested decision (§6.2 says why each was not). |
| R-03 probability raised from Medium to High. | `I2C_RDWR` likely does not check ownership (to confirm, #15). |
| ADR-0001 gains option E (sysfs unbind) and states the per-deployment cost of A. | A's cost falls on every Pi running beacon, not "once". |
| G-11 gains the concrete reset trigger per mode. | "Re-verify after a transaction error" missed a reset that produces no error. |
| RI-01…RI-03 added. | The beacon contract was implied, not stated. |
| RNF-09 resolved (Go 1.26); R-09 and R-10 resolved. | `go.mod` and the module path exist (#54). |
| Binding rule and §9 added; milestones mapped to M0–M6 with M1 ∥ M2. | Hardware recon no longer blocks work that does not need the Pi. |

### Revisions after v0.2

| Date | Change | Why |
| --- | --- | --- |
| 2026-10-16 | Appendix A: operating ranges and LPS25H/HB averaging moved to Confirmed from the datasheets; the averaging conflict settled; overlay rows corrected to what the board shows (nodes declared, no driver bound on 6.12.62) | Datasheets read from distributor copies with document id and page; board facts from M1; detail in [docs/hardware.md](docs/hardware.md) (#17) |
| 2026-10-12 | Appendix A: LPS25H temperature formula, pressure sign, LPS25HB identity and the auto-increment bit moved to Confirmed; averaging sample counts recorded as a conflict between sources | Same method as the HTS221 row; detail in [docs/hardware.md](docs/hardware.md) (#17, partly) |
| 2026-10-12 | Appendix A: HTS221 data, calibration and the auto-increment bit moved to Confirmed | Read from the kernel driver and ST's reference header, not from memory; detail in [docs/hardware.md](docs/hardware.md) (#17, partly) |

**ADR numbering, v0.1 → v0.2:** 0001 → 0001 · 0002 → 0002 · 0005 → 0003 ·
0006 → 0004 · 0007 → 0005 · 0008 → 0006 · 0012 → 0007 · 0003, 0004, 0009, 0010 →
`docs/architecture.md` · 0011 → deferred.

---

## Appendix A — Hardware facts

| Item | Value | Status | Source |
| --- | --- | --- | --- |
| HTS221: address | 0x5F | Confirmed | `rpi-sense` overlay; Rust `sensehat` driver |
| HTS221: WHO_AM_I | register 0x0F = 0xBC | Confirmed | Reference drivers (Nordic, mbed) and register map |
| HTS221: data | humidity at 0x28, temperature at 0x2A (16-bit signed, little-endian) | Confirmed | Kernel `hts221_core.c`; [docs/hardware.md](docs/hardware.md) |
| HTS221: calibration | block from 0x30 (H0_rH_x2 at 0x30, T0_degC_x8 at 0x32, T0/T1 high bits at 0x35, `*_OUT` at 0x36, 0x3A, 0x3C, 0x3E) | Confirmed | Kernel `hts221_core.c` and ST `hts221_reg.h`; [docs/hardware.md](docs/hardware.md) |
| HTS221: overlay | node exists but **disabled** | Confirmed | `rpi-sense-overlay.dts`, `rpi-sense-v2-overlay.dts` (rpi-6.12.y) |
| LPS25H: address | 0x5C | Confirmed | Overlay; Rust driver |
| LPS25H: WHO_AM_I | register 0x0F = 0xBD | Confirmed | ST header (with an internal inconsistency in a comment) and reference code |
| LPS25HB: WHO_AM_I | 0xBD, and the registers vane uses match the LPS25H's | Confirmed | ST `lps25hb_reg.h` against the kernel's LPS25H block; [docs/hardware.md](docs/hardware.md) |
| LPS25H: pressure | 24 bits from 0x28, **signed**; hPa = value / 4096 | Confirmed | ST header and reference code; kernel channel `sign = 's'` |
| LPS25H: BDU and ONE_SHOT | BDU at bit 2 of CTRL_REG1; ONE_SHOT at bit 0 of CTRL_REG2; rates one-shot, 1, 7, 12.5, 25 Hz | Confirmed | ST header |
| LPS25H: temperature | 42.5 + value / 480 (°C), 16-bit signed | Confirmed | ST `lps25hb_reg.c`; kernel `st_pressure_core.c`; [docs/hardware.md](docs/hardware.md) |
| LPS25H: overlay | IIO node declared (`st,lps25h-press`); whether a driver binds depends on the kernel build — none on 6.12.62, whose image ships no `st_pressure` module | Confirmed | Official overlays; the board, [docs/hardware.md](docs/hardware.md) |
| Sense HAT v2: chips | HTS221, LPS25HB, TCS3400 colour sensor | Probable (board: a device answers at 0x39; `RES_CONF` reads the LPS25HB reset value 0x0F) | Reseller listings; Raspberry Pi change notice; the board, [docs/hardware.md](docs/hardware.md) |
| Sense HAT v2: kernel support | from 2022-10-31, version 5.15.74 | Confirmed | Raspberry Pi product change notice (PCN) |
| LSM9DS1 | accel/gyro 0x6A, magnetometer 0x1C; IIO nodes declared in the overlay, no driver bound on 6.12.62 | Confirmed | Official overlays; the board |
| LSM9DS1: WHO_AM_I | 0x68 (accel/gyro), 0x3D (mag) | To confirm | Memory |
| TCS3400 | 0x29 or 0x39 by variant; ID at 0x92 (0x90 or 0x93) | Probable | ams flyer; ChromiumOS driver; the official Python library probes both |
| TCS3400 in the overlay | no node declared | Confirmed | v2 overlay |
| LED/joystick microcontroller | 0x46; EEPROM write-protect register | Confirmed | Official Sense HAT documentation |
| Pi 5: header bus | GPIO2/3 = i2c1 → `/dev/i2c-1` | Confirmed | pinout.xyz; `i2cdetect -l` on the board |
| ST block-read auto-increment bit | MSB of the sub-address, for reads and writes | Confirmed (HTS221 and LPS25H) | Kernel `hts221_i2c.c`; kernel `st_sensors_i2c.c` for the LPS25H |
| HTS221: operating range | 0–100 %RH, −40…+120 °C | Confirmed | DocID026333 Rev 4, Table 3 p. 8 |
| LPS25H/HB: operating range | 260–1260 hPa, −30…+105 °C | Confirmed | DocID023722 Rev 6 Table 3 p. 7; DocID027112 Rev 4 Table 3 p. 9 |
| LPS25H/HB: averaging | `AVGP` 8, 32, 128, 512; `AVGT` 8, 16, 32, 64 (ST's header is wrong for `AVGP`); reset `RES_CONF` 0x05 (LPS25H), 0x0F (LPS25HB) | Confirmed, bit positions excepted | DocID023722 Rev 6 Tables 16–17 p. 24; DocID027112 Rev 4 Tables 18–19 p. 34; [docs/hardware.md](docs/hardware.md) |

## Appendix B — Sanity check on the Pi 5

Corrects the script circulated before the review: with the default overlay
active, the LPS25H does **not** answer a plain `i2cget`, because the kernel owns
it.

```bash
# 1. Board and OS
uname -r
cat /etc/os-release | head -3
for f in product vendor product_ver; do printf '%s: ' "$f"; tr -d '\0' < /proc/device-tree/hat/$f 2>/dev/null || printf 'absent'; echo; done

# 2. Packages and access
sudo apt install -y i2c-tools
ls -l /dev/i2c-*            # owning group (i2c on Raspberry Pi OS)
id                          # is your user in that group?

# 3. Buses and addresses
i2cdetect -l
i2cdetect -y 1              # expected: 1c, 46, 5c, 6a as UU; 5f free; 29/39 on v2 only

# 4. Who owns each address
for a in 001c 0046 005c 005f 006a; do
  d=/sys/bus/i2c/devices/1-$a/driver
  if [ -e "$d" ]; then echo "$a -> $(basename "$(readlink -f "$d")")"; else echo "$a -> no driver"; fi
done
cat /sys/bus/iio/devices/iio:device*/name 2>/dev/null

# 5. Identity of the free chip
i2cget -y 1 0x5f 0x0f       # HTS221: expected 0xbc
```

What to look for:

- `0x5c` (LPS25H) and `0x1c`/`0x6a` (LSM9DS1) as `UU` confirms G-01.
- If `0x5c` shows as a plain address, the overlay was not loaded; note why.
- Never use `i2cget -f` or `i2cset -f` on an address with a bound driver.
- On a v2, look for the colour sensor at `0x29` **and** `0x39`.

## Appendix C — Sources

- [Official Sense HAT documentation (Raspberry Pi)](https://www.raspberrypi.com/documentation/accessories/sense-hat.html)
- [`rpi-sense` overlay (raspberrypi/linux, rpi-6.12.y)](https://github.com/raspberrypi/linux/blob/rpi-6.12.y/arch/arm/boot/dts/overlays/rpi-sense-overlay.dts)
- [`rpi-sense-v2` overlay (raspberrypi/linux, rpi-6.12.y)](https://github.com/raspberrypi/linux/blob/rpi-6.12.y/arch/arm/boot/dts/overlays/rpi-sense-v2-overlay.dts)
- [PCN — Sense HAT revision 2 (Raspberry Pi Ltd)](https://pip.raspberrypi.com/categories/884-pcn/documents/RP-004537-PC/PCN-Sense-HAT-revision-2.pdf)
- [Official Python library — `colour.py`](https://github.com/astro-pi/python-sense-hat/blob/master/sense_hat/colour.py)
- [Issue #143: colour sensor on the Pi 5](https://github.com/astro-pi/python-sense-hat/issues/143)
- [Issue #136: `/dev/i2c-1` permissions on Ubuntu](https://github.com/astro-pi/python-sense-hat/issues/136)
- [ST LPS25H header (mbed)](https://os.mbed.com/teams/Robotique-FIP/code/FIP_REV1//file/69a35a56ac48/Sensor_Shield/lps25h.h)
- [Nordic HTS221 driver (Thingy:52)](https://nordicsemiconductor.github.io/Nordic-Thingy52-FW/documentation/drv__hts221_8h_source.html)
- [HTS221 register map (MrT)](https://mrt.readthedocs.io/en/latest/moddocs/Devices/Sensors/HTS221/README.html)
- [AE-LPS25HB (Akizuki)](https://akizukidenshi.com/goodsaffix/ae-lps25hb.pdf)
- [TCS3400 flyer (ams)](https://www.mouser.com/datasheet/2/588/TCS3400_FS000200_1_00-3044167.pdf)
- [TCS3400 driver (ChromiumOS EC)](https://chromium.googlesource.com/chromiumos/platform/ec/+/master/driver/als_tcs3400.h)
- [Sense HAT V2 — reseller listing (PiShop)](https://www.pishop.us/product/raspberry-pi-sense-hat-v2/)
- [periph.io conn/v3](https://pkg.go.dev/periph.io/x/conn/v3) and [host/v3](https://pkg.go.dev/periph.io/x/host/v3)
- [pinout.xyz — I2C](https://pinout.xyz/pinout/i2c)
- [HTS221 thread with identical calibration values (ST Community)](https://community.st.com/mems-sensors-48/hts221-h0-t0-out-and-h1-t0-out-h0-rh-and-h1-rh-returns-same-value-156039)
