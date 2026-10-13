# Architecture — vane

Packages, public signatures without bodies, and the flows that matter, each step
marked with the requirement it enforces. Written before the code so design
problems surface while they are cheap; once code exists, **the code is
authoritative and this document is updated in the same pull request** that
changes a signature.

Companion documents: [`REQUIREMENTS.md`](../REQUIREMENTS.md) (what and why),
[`adr/`](adr/README.md) (contested decisions). Decisions that had no alternative
worth an ADR are recorded in [§7](#7-decisions-recorded-here).

---

## 1. Packages and dependency direction

```
                     cmd/vane-probe
                           │
                           ▼
                       sensehat ─────────────────┐
                      │        │                 │
                      ▼        ▼                 ▼
            chip/hts221    chip/lps25h         i2cdev ──► golang.org/x/sys/unix
                 │   │        │   │              │
                 │   └──► internal/regio ◄──┘    │
                 │        internal/poll          │
                 ▼                               ▼
                 └──────────────► vane ◄─────────┘        (leaf)
                                   ▲
              vanetest ────────────┘  (+ sensehat, for FakeBoard only)

      internal/kernel ◄── sensehat (sysfs ownership, HAT EEPROM)
```

| Rule | Why | Enforced by |
| --- | --- | --- |
| `vane` imports no package of this module | Drivers import `vane`, `sensehat` imports drivers; anything else is a cycle (G-03) | `check-boundaries.sh` §3 |
| Only `i2cdev` imports `golang.org/x/sys` | Everything else compiles and tests on any `GOOS` (RNF-02, RNF-03) | `check-boundaries.sh` §1 |
| No module but `x/sys` in the graph | The one admitted dependency (RNF-03, RI-02) | `check-boundaries.sh` §2 |
| `vanetest` does not import `chip/*` | A test oracle sharing the driver's constants replicates its mistakes (RF-09) | `check-boundaries.sh` §4 |
| Drivers import `vane` and `internal/*` only | A driver knows its chip, not the board or the OS | review; a guard when the first driver lands |
| `sensehat` is the only package that knows the board | Addresses, the probe table and kernel ownership live in one place | review |

`internal/` packages are not importable by consumers, so their signatures can
change without a SemVer event.

---

## 2. Package `vane` — shared types

```go
package vane

// Units are named types so the compiler rejects mixing them (RF-04).
type Celsius float64
type RelativeHumidity float64 // percent; 0–100 after clamping (RF-12)
type Hectopascal float64

type Chip string

const (
	ChipHTS221 Chip = "HTS221"
	ChipLPS25H Chip = "LPS25H" // LPS25H/LPS25HB family (ADR-0003)
)

// Source identifies where a measurement came from.
type Source struct {
	Chip    Chip
	Bus     string // "/dev/i2c-1", or a fake's name
	Address uint16
}

// Flag describes the quality of a measurement. Flags never become errors (RF-12).
type Flag uint8

const (
	FlagClamped    Flag = 1 << iota // converted value was outside the physical range and was clamped
	FlagOutOfRange                  // outside the datasheet's operating range; value kept as converted
	FlagStale                       // continuous mode: no new sample since the previous read
	FlagOverrun                     // continuous mode: the chip overwrote an unread sample
)

func (f Flag) Has(x Flag) bool
func (f Flag) String() string // "clamped|stale"; "" for none

// Measurement is a value object; it is never mutated after the driver returns it.
type Measurement[T ~float64] struct {
	Value  T
	Raw    int32     // ADC count before conversion, sign-extended (G-04)
	Source Source
	Time   time.Time // taken after data-ready, from the injected clock (G-05)
	Flags  Flag
}

// Bus is the only hardware seam (ports and adapters). Implementations must be
// safe for concurrent use and must perform w then r as ONE combined
// transaction with a repeated start; a nil or empty w or r means "no write" or
// "no read". Tx must check ctx before starting and must not retry.
type Bus interface {
	Tx(ctx context.Context, addr uint16, w, r []byte) error
}
```

### Errors (RNF-06, ADR-0006)

```go
var (
	ErrNotPresent          = errors.New("vane: no device at address")         // NACK on the identity read
	ErrUnexpectedChip      = errors.New("vane: unexpected chip")              // RF-01
	ErrKernelOwned         = errors.New("vane: device is owned by a kernel driver") // RF-07
	ErrPermission          = errors.New("vane: permission denied on the bus device")
	ErrCalibration         = errors.New("vane: degenerate factory calibration") // G-12
	ErrNotReady            = errors.New("vane: data not ready in time")       // also the reset symptom (G-11)
	ErrClosed              = errors.New("vane: closed")                       // RF-08
	ErrUnsupportedPlatform = errors.New("vane: unsupported platform")         // G-16
)

// SensorError wraps any failure with where and during what it happened.
type SensorError struct {
	Source Source
	Op     string // "identify", "calibrate", "configure", "trigger", "wait", "read", "close"
	Reg    uint8  // register involved, meaningful only when HasReg is set
	HasReg bool   // the zero value means "no register", not register 0x00
	Err    error
}
func (e *SensorError) Error() string
func (e *SensorError) Unwrap() error

type IdentityError struct {
	Source        Source
	Register      uint8
	Want, Got     uint8
}
func (e *IdentityError) Error() string
func (e *IdentityError) Is(target error) bool // target == ErrUnexpectedChip

type KernelOwnedError struct {
	Source Source
	Driver string // name read from sysfs, e.g. "st-press-i2c"
}
func (e *KernelOwnedError) Error() string
func (e *KernelOwnedError) Is(target error) bool // target == ErrKernelOwned
```

---

## 3. Chip drivers — `chip/hts221`, `chip/lps25h`

Both expose the same shape; the HTS221 is shown, the LPS25H differences follow.
The HTS221 is implemented; this is its real API.

```go
package hts221

const DefaultAddress = 0x5F

type Sensor struct{ /* regio.Dev, source, config, calibration, mu, scratch, clock, closed, reinit, dirty */ }

// New identifies, calibrates and configures, in that order. The ORDER is the
// requirement: nothing is written before identification succeeds (RF-01), and a
// chip whose calibration is unusable is never configured.
func New(ctx context.Context, bus vane.Bus, opts ...Option) (*Sensor, error)

// Read returns one sample. One-shot (default): trigger → wait for both
// data-ready bits → read, under the sensor's mutex (RNF-04). Continuous: read
// the latest sample, flagged FlagStale per quantity when its data-ready bit is
// clear. ErrNotReady after a trigger means the chip may have reset: the NEXT
// Read identifies and configures again. Read never retries (G-11).
func (s *Sensor) Read(ctx context.Context) (Sample, error)

func (s *Sensor) Source() vane.Source
func (s *Sensor) Calibration() Calibration
func (s *Sensor) Close(ctx context.Context) error // writes CTRL_REG1 = BDU (power-down, BDU kept on); idempotent

type Sample struct {
	Humidity    vane.Measurement[vane.RelativeHumidity]
	Temperature vane.Measurement[vane.Celsius]
}

type Calibration struct {
	H0RH, H1RH     float64 // %RH at the two points
	T0DegC, T1DegC float64 // °C at the two points
	H0T0Out, H1T0Out, T0Out, T1Out int16
}
func (c Calibration) Validate() error // ErrCalibration: equal counts or equal references (G-12)

type Mode struct{ /* zero value is one-shot */ }
func OneShot() Mode
func Continuous(r Rate) Mode
type Rate uint8 // Rate1Hz, Rate7Hz, Rate12Hz5: the ODR code in CTRL_REG1

// 0 keeps the chip default for that half: humidity 32, temperature 16.
type Averaging struct{ Humidity, Temperature uint16 } // 4…512 and 2…256, powers of two

type Option func(*config)
func WithAddress(addr uint16) Option
func WithBusName(name string) Option
func WithMode(m Mode) Option
func WithAveraging(a Averaging) Option
func WithClock(now func() time.Time) Option
func WithPollInterval(d time.Duration) Option // default 5 ms
func WithReadyTimeout(d time.Duration) Option // default 1 s: an over-estimate, conversion times unconfirmed
```

**Recovery, and why it asks the chip.** After a wait ends in `ErrNotReady` the
next `Read` identifies, reconfigures, then reads `CTRL_REG2`: `ONE_SHOT` is 1
while a conversion is pending. A chip that reset reads 0 and holds nothing; one
that merely stalled still reads 1, and the `Read` lets that conversion end and
drains its sample before it triggers, because a second trigger on top of it is
two sequences on one chip (ADR-0004). The same settling happens after a `Read`
cut short by the context. (That `ONE_SHOT` reads back this way is listed in
`docs/hardware.md` as still to confirm.)

**LPS25H/LPS25HB** (`chip/lps25h`, `DefaultAddress = 0x5C`) is implemented, with
the same shape and these differences:

```go
type Sample struct {
	Pressure    vane.Measurement[vane.Hectopascal] // Raw: 24-bit, sign-extended
	Temperature vane.Measurement[vane.Celsius]     // Raw: 16-bit signed
}

// Rate1Hz, Rate7Hz, Rate12Hz5, Rate25Hz: the ODR code, in bits 6:4 of CTRL_REG1.
func (s *Sensor) Variant() Variant // always VariantUnknown: nothing readable tells the chips apart
```

- **No `Calibration`**: the chip is factory-trimmed. No `Averaging` and no
  `WithAveraging`: the sample counts per `AVGP`/`AVGT` code are a conflict
  between sources (`docs/hardware.md`), so `RES_CONF` is left as the chip has it
  and `New` writes `CTRL_REG1` and nothing else.
- **Flags**: `FlagOverrun` can be set (the status register has `P_OR` and
  `T_OR`), per quantity, in continuous mode; `FlagStale` likewise from the
  data-ready bits; `FlagOutOfRange` for pressure outside 260–1260 hPa and
  temperature above 105 °C; `FlagClamped` is never set. There is no lower
  temperature bound: a 16-bit count at 480 per degree around 42.5 °C cannot go
  below −25.77 °C.
- **Pressure sign**: the 24-bit output is two's complement, and
  `Raw` is sign-extended so a differential reading below zero stays negative.
- `Variant` is a type with one value, `VariantUnknown`. `VariantH` and
  `VariantHB` are added if the board shows a difference that can be read without
  side effects; adding them does not break callers.

The two drivers repeat the same recovery, settle and close logic. Two copies
cost less than a shared engine that would need a callback for every difference
(status bits, output length, configuration, conversion); a third chip would
change that.

### What a driver must not do

- Write before `WHO_AM_I` matches (RF-01).
- Configure without BDU (RF-06).
- Read more than one byte without going through `regio.ReadBlock` (G-13).
- Retry, sleep without honouring `ctx`, log, or start a goroutine.

---

## 4. Board facade — `sensehat`

Implemented. `Diagnose` and `vane-probe` (#41) come next.

```go
package sensehat

func Open(ctx context.Context, opts ...Option) (*Device, error)
func (d *Device) Environment(ctx context.Context) (Environment, error)
func (d *Device) Sensors() []SensorInfo // a copy; one entry per chip on the board, in a fixed order
func (d *Device) Board() BoardInfo
func (d *Device) Close() error          // idempotent; the first call returns the errors

type Environment struct {
	Humidity     Reading[vane.RelativeHumidity]
	Pressure     Reading[vane.Hectopascal]
	Temperatures []Reading[vane.Celsius] // one per requested chip, HTS221 first (RI-03)
}

// Err == nil ⇔ the Measurement is valid. A failed Reading still carries its Source.
type Reading[T ~float64] struct {
	vane.Measurement[T]
	Err error
}

type State uint8 // StateReady, StateFailed, StateKernelOwned, StateNotRequested
type SensorInfo struct {
	Source vane.Source
	State  State
	Err    error // why the chip is not ready
}
type BoardInfo struct {
	Present                     bool
	Product, Vendor, ProductVer string
}

var ErrNotRequested error // the Err of a chip WithChips left out

// Options: WithBus, WithBusPath, WithChips, WithRequired, WithHTS221, WithLPS25H,
// WithClock, WithKernelCheck, WithRoot, WithCloseTimeout.
```

**Decisions made while building it** (none contested enough for an ADR):

- **The board's addresses are fixed.** The kernel check needs a chip's address
  *before* its driver exists, because building a driver already reads the chip.
  `WithHTS221(opts...)` passes opaque options, `WithAddress` among them, so the
  facade appends its own `WithAddress` **last** and it wins. A different layout
  is not a Sense HAT; use the drivers directly.
- **The kernel check fails closed.** A bus path that is not `/dev/i2c-N` has no
  sysfs node, and skipping the check silently would be failing open, so `Open`
  refuses unless `WithKernelCheck(false)` says the bus is not a kernel adapter.
  A check that errors (not "no such path") marks the chip failed, never "not
  owned".
- **`WithBusPath` always names the adapter**, even with `WithBus`: it is what the
  kernel check and each `Source` use. `WithBus` only replaces the opening, and
  then the caller keeps the bus; `Close` closes only a bus `Open` opened.
- **`WithRoot`** (not `WithSysfsRoot`) because it moves the device tree as well as
  sysfs.
- **`WithCloseTimeout` is per chip.** One shared deadline let a slow chip use up
  the budget and leave every later chip powered up; a test caught it. It bounds
  the power-down transaction, not the wait for a `Read` in progress.
- **A context that has ended needs no check in `Environment`**: the first
  transaction of each driver does it, as the `vane.Bus` contract requires.
- **Closing after a failed or cancelled `Open`** uses `context.WithoutCancel`:
  the context that interrupted `Open` is the one that has ended, and the chips it
  opened still have to be powered down.

### The unexported adapter

```go
// adapter drives one chip for the facade. Each chip fills different fields of an
// Environment, whose types differ, so a small adapter per chip beats a switch.
type adapter interface {
	open(ctx context.Context, bus vane.Bus, c *config) error
	fill(ctx context.Context, env *Environment, err error) error // the error the readings carry, or nil
	absent(env *Environment)
	close(ctx context.Context) error
}
```

It stays unexported: with two chips there is nothing to gain from letting a
caller supply a third, and exporting it would freeze a shape before the IIO
backend (v0.2), which may need a different one, exists. The set of chips is a
plain `probeTable` value — address, chip, constructor — that nothing registers
into.

## 5. Backends and test doubles

### `i2cdev` (Linux)

```go
//go:build linux
package i2cdev

type Bus struct{ /* fd int, path string, mu sync.Mutex */ }
func Open(path string) (*Bus, error)                                    // O_RDWR|O_CLOEXEC; EACCES → vane.ErrPermission
func (b *Bus) Tx(ctx context.Context, addr uint16, w, r []byte) error   // one I2C_RDWR ioctl, 1 or 2 messages
func (b *Bus) Name() string
func (b *Bus) Close() error
```

A stub file (`//go:build !linux`) gives the same API returning
`vane.ErrUnsupportedPlatform`. `EREMOTEIO`/`ENXIO` from the ioctl map to
`vane.ErrNotPresent`.

**Why `I2C_RDWR` and never `I2C_SLAVE`/`I2C_SLAVE_FORCE`.** In the kernel the
Pi runs (`raspberrypi/linux` `rpi-6.12.y`, `drivers/i2c/i2c-dev.c`), the
ownership check `i2cdev_check_addr` is called only for `I2C_SLAVE` (line 414);
`I2C_SLAVE_FORCE` skips it, and `I2C_RDWR` (`i2cdev_ioctl_rdwr`, line 243) goes
straight to `i2c_transfer` with no check at all. So `I2C_RDWR` **would** reach a
kernel-owned chip silently — R-03 is real, and the sysfs check in
`internal/kernel` is the only thing that prevents it (RF-07). `I2C_RDWR` is
still the right ioctl: it carries the address per message (no per-fd state) and
does write-then-read with a repeated start in one call.

### `vanetest`

```go
package vanetest

// FakeBus routes by address, serialises transactions as a real bus does, logs
// every one, and injects faults. Safe for concurrent use.
func NewFakeBus(devices ...Device) *FakeBus
func (b *FakeBus) Tx(ctx context.Context, addr uint16, w, r []byte) error
func (b *FakeBus) Inject(addr uint16, f Fault)
func (b *FakeBus) Clear(addr uint16)        // drops faults, releases a stalled data-ready
func (b *FakeBus) Log() []Tx                // deep copies; Tx{Addr, W, R, Err}
func (b *FakeBus) Writes(addr uint16) [][]byte // for "nothing written before WHO_AM_I"

type Device interface {
	Addr() uint16
	Tx(w, r []byte) error
}

// Faults. After(n) lets n transactions through first; Times(n) bounds how many
// are affected (0 = until Clear). StallReady and Reset are events: Times(1).
func NACK() Fault                    // vane.ErrNotPresent; the device never sees it
func EIO() Fault                     // ErrIO; the device never sees it
func ShortRead() Fault               // device runs, ErrShortRead, tail of r zeroed
func Latency(d time.Duration) Fault  // holds the bus; the context can cut it
func StallReady() Fault              // calls ReadyStaller.StallReady(true) once
func Reset() Fault                   // calls Resetter.Reset() once, no bus error

type Resetter interface{ Reset() }
type ReadyStaller interface{ StallReady(stalled bool) }

// Regs is the register file the chip models are built on. It enforces the
// datasheet rules a real chip punishes silently, and reports each violation to
// the test (Reporter, satisfied by *testing.T) as well as returning
// ErrProtocol, so a driver that swallows the error still fails:
//   - a write to a register outside the writable set (ADR-0007);
//   - a multi-byte read or write without the auto-increment bit (G-13);
//   - an access past the last register.
func NewRegs(rep Reporter, addr uint16, writable ...uint8) *Regs
func (r *Regs) Get(reg uint8) uint8
func (r *Regs) Set(reg, val uint8)
func (r *Regs) Tx(w, rd []byte) error

// Chip models (#25, #31) embed Regs, are written from the datasheet and import
// nothing from chip/*. The rule that a trigger → wait → read sequence is not
// interleaved with another is the model's, not the bus's: only the model knows
// what a sequence is.
func NewHTS221(opts ...ModelOption) *HTS221Model
func (m *HTS221Model) Set(rh vane.RelativeHumidity, t vane.Celsius)
func NewLPS25H(opts ...ModelOption) *LPS25HModel
func (m *LPS25HModel) Set(p vane.Hectopascal, t vane.Celsius)

// Recorder wraps a bus and writes the transcript; a transaction cut short by
// the context is not recorded. Comment adds the '#' metadata lines.
func NewRecorder(inner vane.Bus, w io.Writer) *Recorder
func (r *Recorder) Comment(format string, args ...any)
func (r *Recorder) Err() error

// ReplayBus requires each transaction to match the next recorded one (address,
// bytes written, length read). The first divergence is a *DivergenceError,
// reported to the test and sticky; a transcript not fully replayed is reported
// when the test ends.
func NewReplayBus(t T, transcript io.Reader) *ReplayBus // T: Helper, Errorf, Cleanup
func (b *ReplayBus) Remaining() int

// FakeBoard is *sensehat.Device's method set, answering from a script.
func NewFakeBoard(script ...Step) *FakeBoard
func (f *FakeBoard) Environment(ctx context.Context) (sensehat.Environment, error)
func (f *FakeBoard) Sensors() []sensehat.SensorInfo // both chips ready until SetSensors
func (f *FakeBoard) Board() sensehat.BoardInfo      // absent until SetBoard
func (f *FakeBoard) Close() error                   // idempotent
func (f *FakeBoard) SetSensors(infos ...sensehat.SensorInfo)
func (f *FakeBoard) SetBoard(b sensehat.BoardInfo)
func (f *FakeBoard) Closed() bool
func (f *FakeBoard) Calls() int

// Steps. Healthy fills every field a Device does; Failing wraps the chip's
// error in a SensorError and, when every reading has failed, sets the top-level
// error as a Device does. Past the end of the script, Environment returns
// ErrScriptEnded, so a consumer that polls more than planned is noticed.
func Healthy(at time.Time, rh vane.RelativeHumidity, p vane.Hectopascal, tHTS221, tLPS25H vane.Celsius) Step
func Result(env sensehat.Environment, err error) Step
func Fail(err error) Step
func (s Step) Failing(chip vane.Chip, err error) Step
func (s Step) Delayed(d time.Duration) Step
func (s Step) Times(n int) Step
func (s Step) Forever() Step
```

A test file in `vanetest` declares the method set beacon depends on and asserts
both `*sensehat.Device` and `*FakeBoard` satisfy it, so they cannot drift
(RI-01).

### Internal packages

```go
package regio // register access for ST chips (G-13)
// Dev owns a scratch buffer so reads allocate nothing (RNF-08): a buffer local
// to a function escapes through the Bus interface (measured, 2 allocs/op).
// Not safe for concurrent use; the driver's per-chip mutex covers it (ADR-0004).
type Dev struct{ /* bus, addr, buf [2]byte */ }
func New(b vane.Bus, addr uint16) *Dev
func (d *Dev) Read(ctx context.Context, reg uint8) (uint8, error)
func (d *Dev) Write(ctx context.Context, reg, val uint8) error
func (d *Dev) ReadBlock(ctx context.Context, reg uint8, dst []byte) error // sets 0x80; the only place that does

package poll // data-ready wait (RNF-05)
type Clock interface { Now() time.Time; Sleep(ctx context.Context, d time.Duration) error }
var Real Clock // wall clock; tests inject one that advances virtual time
// nil clk means Real. ErrNotReady at the timeout, ctx.Err() on cancellation;
// ready is called once more at the deadline.
func Until(ctx context.Context, clk Clock, interval, timeout time.Duration, ready func(context.Context) (bool, error)) error

package kernel // sysfs and device tree, root injectable for tests
func Owner(root string, bus int, addr uint16) (driver string, owned bool, err error)
func Board(root string) (product, vendor, productVer string, ok bool)
```

---

## 6. Flows

### 6.1 `sensehat.Open`

1. Resolve the bus: `WithBus` or `i2cdev.Open(WithBusPath)`. `EACCES` → `ErrPermission` with the group hint (R-07).
2. Read `BoardInfo` from `/proc/device-tree/hat/` — information only (RF-13, ADR-0003).
3. For each chip in the probe table (filtered by `WithChips`; the others are `StateNotRequested` and never touched):
   1. **Kernel ownership first** (RF-07): `kernel.Owner` → `KernelOwnedError`, state `StateKernelOwned`. No bus traffic to that address. A check that cannot answer is `StateFailed`, never "not owned".
   2. `New` the driver (§6.2). Failure → state `StateFailed`, error kept.
4. If a chip named in `WithRequired` failed → close what opened, return the joined errors (G-06).
5. If no chip opened → close, return `errors.Join` of all (ADR-0006).

### 6.2 Driver `New` — the order is the requirement

1. Read `WHO_AM_I`. NACK → `ErrNotPresent`. Mismatch → `IdentityError`. **No write has happened yet** (RF-01).
2. HTS221 only: `ReadBlock` the calibration, `Validate` → `ErrCalibration` (RF-02, G-12).
3. Write configuration: power-up, **BDU = 1** (RF-06), averaging, mode/rate.
4. Every write goes to a register the chip's model allows (ADR-0007, checked in tests).

Code with steps 1 and 3 swapped looks identical in review; the test asserts
that the first write in the recorded sequence comes after the `WHO_AM_I` read.

### 6.3 One-shot `Read`

1. Lock the sensor's mutex — trigger → wait → read is atomic per chip (RNF-04).
2. `ctx` checked; write `ONE_SHOT` through the sensor's `regio.Dev`.
3. `poll.Until` on the status register, `ctx` checked each iteration (RNF-05).
   Timeout → `ErrNotReady`; mark the sensor for re-initialisation (G-11).
4. `ReadBlock` the data registers; sign-extend into `Raw`; convert; clamp and flag (RF-12).
5. `Time` from the injected clock, after data-ready (G-05).

On the next `Read` after `ErrNotReady`, the driver re-runs §6.2 before
triggering — a chip that browned out comes back without the caller doing
anything. It does not retry within the same call (RI-02: retry is the
consumer's).

### 6.4 `Environment` — partial failure

1. `ctx` and closed checks → top-level error only for these.
2. Each source fills its own fields, sequentially (no goroutines in v0.1).
   A failure becomes that `Reading`'s `Err`.
3. If **every** reading failed → `errors.Join` as the top-level error; otherwise nil.

### 6.5 `Close`

Idempotent. Each driver writes power-down (RF-08, G-19) within
`WithCloseTimeout`; the bus is closed only if `Open` opened it. Every later call
returns `ErrClosed`.

---

## 7. Decisions recorded here

Not contested enough for an ADR (REQUIREMENTS §6.2), but each had an
alternative, so the reason is kept.

| Topic | Decision | Rejected, and why | Reopen when |
| --- | --- | --- | --- |
| Module layout | One module in v0.x | Separate `vanetest` or `i2cdev` modules: multi-module tags and a workspace that hides stale pins, for no dependency to isolate | An adapter brings a heavy dependency (e.g. `periphbus`) |
| Units | Named `float64` types + generic `Measurement[T]` | `periph.io/x/conn/v3/physic`: a dependency, and fixed-point types the consumer must convert anyway | Never in v0.x |
| Go floor | Go 1.26 (`go.mod`) | An older `x/sys` for a lower floor: buys support for a toolchain no longer patched | `x/sys` raises its own floor |
| Transcript format | Text, below | JSON or binary: unreadable in a pull request diff, which is the point of recording | A field the line format cannot carry |
| Hooks | None; consumers decorate `vane.Bus` | A `Hooks` struct in `vane`: API surface no consumer has asked for | A consumer needs per-read timing inside the per-chip sequence (#49) |
| Concurrency inside a read | Sequential sources in `Environment` | One goroutine per chip: two chips share one bus, which the kernel serialises anyway; no latency to win | Measured `Environment` latency matters to beacon |

### Transcript format (RF-11)

```
# vane transcript v1
# bus=/dev/i2c-1 board="Sense HAT" product_ver=0x0002 kernel=6.12.47 recorded=2026-10-20T10:00:00Z
5f W 0f R bc                 # HTS221 WHO_AM_I
5f W b0 R 3a 4c ...          # calibration block, 0x30 | 0x80 auto-increment
5f W 20 85                   # write-only: CTRL_REG1 = PD | BDU | 1 Hz
5c W 0f E nack               # error lines: E <nack|eio|short|error>
```

One transaction per line: address, `W` and the written bytes, then `R` and the
read bytes or `E` and an error kind (`nack`, `eio`, `short`, or `error` for any
other). The `# vane transcript v1` header is required, so another kind of file is
refused rather than replayed as nonsense. `#` starts a comment. `ReplayBus` requires the
address and written bytes to match exactly and the read length to match; any
divergence fails the test with both lines printed.
