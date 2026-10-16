package lps25h

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/internal/poll"
	"github.com/JonasBorgesLM/vane/internal/regio"
)

// Sample is one reading of both quantities. They come from one conversion and
// share a Time.
type Sample struct {
	Pressure    vane.Measurement[vane.Hectopascal]
	Temperature vane.Measurement[vane.Celsius]
}

// Variant says which chip of the family a Sensor is driving.
type Variant uint8

// VariantUnknown is the only variant a Sensor reports. The LPS25H and the
// LPS25HB return the same identity value and share every register this package
// uses, and neither the kernel's driver nor ST's reference code reads anything
// that tells them apart. It does not mean they behave identically in registers
// vane does not use. If a difference that can be read without side effects is
// found on the board, VariantH and VariantHB can be added without breaking
// callers; until then there is nothing true to return.
const VariantUnknown Variant = iota

// Sensor is an LPS25H or LPS25HB on a bus. It is safe for concurrent use: the
// mutex makes trigger, wait and read one sequence per chip (ADR-0004).
type Sensor struct {
	dev *regio.Dev
	src vane.Source
	cfg config

	mu     sync.Mutex
	out    [5]byte    // scratch for the output registers; guarded by mu
	clk    poll.Clock // one timer, reused by every wait; guarded by mu
	ready  func(context.Context) (bool, error)
	closed bool
	// reinit: the chip may have lost its configuration (a wait ended in
	// ErrNotReady, which is how a reset shows in one-shot mode), so the next
	// Read identifies and configures it again first.
	reinit bool
	// dirty: a one-shot conversion was triggered and its sample not read,
	// because the read ended early. The next Read lets it finish first.
	dirty bool
}

// New identifies the chip, then configures it. The order is the requirement:
// nothing is written before the identity register has been read and matched
// (RF-01), because a write to the wrong device is not harmless and the same bus
// carries the HAT's microcontroller.
//
// New does not touch the averaging register. Its sample counts per code are
// unconfirmed (docs/hardware.md), so the chip keeps whatever averaging it has.
func New(ctx context.Context, bus vane.Bus, opts ...Option) (*Sensor, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := newSensor(bus, cfg)
	// Built once: a method value made per call would allocate on every Read.
	s.ready = s.dataReady
	s.clk = poll.Reusable()
	if err := s.identify(ctx); err != nil {
		return nil, err
	}
	if err := s.takeOver(ctx); err != nil {
		return nil, err
	}
	if err := s.configure(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// newSensor builds the Sensor New and Identify both start from.
func newSensor(bus vane.Bus, cfg config) *Sensor {
	return &Sensor{
		dev: regio.New(bus, cfg.addr),
		src: vane.Source{Chip: vane.ChipLPS25H, Bus: cfg.bus, Address: cfg.addr},
		cfg: cfg,
	}
}

// Identify reads the chip's identity register and does nothing else: no
// calibration, no configuration, and above all no write. It is how a diagnostic
// asks whether the chip is there and is the right one without changing its
// state, which New cannot do since it powers the chip up. The error is the one
// New returns at that step: a *vane.SensorError if the bus failed or nothing
// acknowledged, and a *vane.IdentityError if something else answered.
func Identify(ctx context.Context, bus vane.Bus, opts ...Option) error {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	return newSensor(bus, cfg).identify(ctx)
}

// Source describes where this sensor's measurements come from.
func (s *Sensor) Source() vane.Source { return s.src }

// Variant reports which chip of the family this is: always VariantUnknown, for
// the reason given there.
func (s *Sensor) Variant() Variant { return VariantUnknown }

// identify reads the identity register. A bus error, including no
// acknowledgement, is a *vane.SensorError; a wrong value is a
// *vane.IdentityError.
func (s *Sensor) identify(ctx context.Context) error {
	id, err := s.dev.Read(ctx, regWhoAmI)
	if err != nil {
		return s.fail("identify", regWhoAmI, err)
	}
	if id != whoAmIValue {
		return &vane.IdentityError{Source: s.src, Register: regWhoAmI, Want: whoAmIValue, Got: id}
	}
	return nil
}

// configure powers the chip up in the configured mode, in one write. BDU is on
// in every configuration, in every mode and on every path (RF-06): without it a
// multi-byte read can combine bytes of two different samples. In one-shot mode
// ODR is 0; in continuous mode it is the rate, in bits 6:4. SIM, RESET_AZ and
// DIFF_EN stay clear; differential mode would change what the pressure
// registers mean.
// takeOver deals with what an earlier user of the chip left behind, before New
// configures it, without writing anything (#91, seen on a Pi). An unread sample
// keeps the data-ready bits set under BDU, so the first wait would return at
// once with it and every later read would be one conversion behind: it is read
// out and dropped. A ONE_SHOT latched while the chip was powered down runs as
// soon as configure powers it up: it is marked dirty, so the first Read lets
// it finish and drops it before triggering its own, as after a reinit. The Pi 5
// keeps the HAT powered when halted, so both survive a shutdown.
func (s *Sensor) takeOver(ctx context.Context) error {
	cr2, err := s.dev.Read(ctx, regCtrl2)
	if err != nil {
		return s.fail("configure", regCtrl2, err)
	}
	s.dirty = cr2&ctrl2OneShot != 0
	if err := s.dev.ReadBlock(ctx, regOut, s.out[:]); err != nil {
		return s.fail("configure", regOut, err)
	}
	return nil
}

func (s *Sensor) configure(ctx context.Context) error {
	// Again on a reinit: a reset puts RES_CONF back to the chip's default.
	if s.cfg.avg != (Averaging{}) {
		cur, err := s.dev.Read(ctx, regResConf)
		if err != nil {
			return s.fail("configure", regResConf, err)
		}
		if err := s.dev.Write(ctx, regResConf, s.cfg.avg.resConf(cur)); err != nil {
			return s.fail("configure", regResConf, err)
		}
	}
	ctrl1 := uint8(ctrl1PD | ctrl1BDU)
	if s.cfg.mode.continuous {
		ctrl1 |= uint8(s.cfg.mode.rate) << ctrl1ODRShift
	}
	if err := s.dev.Write(ctx, regCtrl1, ctrl1); err != nil {
		return s.fail("configure", regCtrl1, err)
	}
	return nil
}

func (s *Sensor) fail(op string, reg uint8, err error) error {
	return &vane.SensorError{Source: s.src, Op: op, Reg: reg, HasReg: true, Err: err}
}

func (s *Sensor) closedError(op string) error {
	return &vane.SensorError{Source: s.src, Op: op, Err: vane.ErrClosed}
}

// Read returns one sample. In one-shot mode, the default, it triggers a
// conversion, waits for it and reads the result, so the sample is always new and
// the chip idles between reads. In continuous mode it returns the chip's latest
// sample, with a quantity flagged vane.FlagStale if it is not newer than the
// previous one and vane.FlagOverrun if the chip says it was overwritten.
//
// Read stops waiting when ctx ends, but a transaction already on the bus cannot
// be interrupted, so cancellation takes effect after at most one transaction
// and one poll interval (RNF-05). After Close it returns vane.ErrClosed. If the
// chip stops answering a trigger, Read returns vane.ErrNotReady and the next
// Read configures the chip again, which is how a reset is recovered from; Read
// does not retry, that is the caller's decision (G-11).
func (s *Sensor) Read(ctx context.Context) (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return Sample{}, s.closedError("read")
	}
	if s.reinit {
		if err := s.identify(ctx); err != nil {
			return Sample{}, err
		}
		if err := s.configure(ctx); err != nil {
			return Sample{}, err
		}
		s.reinit = false
		// Whether a conversion is still pending is the chip's to say: ONE_SHOT
		// reads 1 until the conversion ends. A chip that was reset reads 0 and
		// holds nothing; one that merely stalled still reads 1, and a second
		// trigger on top of it would be two sequences on one chip.
		cr2, err := s.dev.Read(ctx, regCtrl2)
		if err != nil {
			return Sample{}, s.fail("configure", regCtrl2, err)
		}
		s.dirty = cr2&ctrl2OneShot != 0
	}
	if s.cfg.mode.continuous {
		return s.readLatest(ctx)
	}
	return s.readOneShot(ctx)
}

func (s *Sensor) readOneShot(ctx context.Context) (Sample, error) {
	if s.dirty {
		// An earlier read ended after the trigger and before the result. Its
		// conversion is still running or finished, and a second trigger on top of
		// it is two sequences on one chip. Let it end and take the sample out.
		if err := s.wait(ctx); err != nil {
			return Sample{}, err
		}
		if err := s.dev.ReadBlock(ctx, regOut, s.out[:]); err != nil {
			return Sample{}, s.fail("read", regOut, err)
		}
		s.dirty = false
	}
	if err := s.dev.Write(ctx, regCtrl2, ctrl2OneShot); err != nil {
		return Sample{}, s.fail("trigger", regCtrl2, err)
	}
	s.dirty = true
	if err := s.wait(ctx); err != nil {
		return Sample{}, err
	}
	now := s.cfg.now()
	if err := s.dev.ReadBlock(ctx, regOut, s.out[:]); err != nil {
		return Sample{}, s.fail("read", regOut, err)
	}
	s.dirty = false
	return s.sample(now, 0, 0), nil
}

// readLatest returns the chip's latest sample. A data-ready bit that is not set
// means that quantity has no sample newer than the one last read; an overrun bit
// that is set means a sample of it was overwritten before it was read.
func (s *Sensor) readLatest(ctx context.Context) (Sample, error) {
	st, err := s.dev.Read(ctx, regStatus)
	if err != nil {
		return Sample{}, s.fail("read", regStatus, err)
	}
	now := s.cfg.now()
	if err := s.dev.ReadBlock(ctx, regOut, s.out[:]); err != nil {
		return Sample{}, s.fail("read", regOut, err)
	}
	var pExtra, tExtra vane.Flag
	if st&statusPDA == 0 {
		pExtra |= vane.FlagStale
	}
	if st&statusPOR != 0 {
		pExtra |= vane.FlagOverrun
	}
	if st&statusTDA == 0 {
		tExtra |= vane.FlagStale
	}
	if st&statusTOR != 0 {
		tExtra |= vane.FlagOverrun
	}
	return s.sample(now, pExtra, tExtra), nil
}

// wait blocks until both data-ready bits are set. A timeout means the chip never
// finished, which after a trigger is what a chip that reset looks like, so the
// next Read configures it again.
func (s *Sensor) wait(ctx context.Context) error {
	err := poll.Until(ctx, s.clk, s.cfg.pollInterval, s.cfg.readyTimeout, s.ready)
	if err == nil {
		return nil
	}
	if errors.Is(err, vane.ErrNotReady) {
		s.reinit = true
	}
	return s.fail("wait", regStatus, err)
}

// Close powers the chip down. It is idempotent, and after it every Read returns
// vane.ErrClosed. BDU stays on even in power-down, like every other write. The
// sensor is closed even if the write fails, and the error is returned once.
//
// Close waits for a Read in progress, which is bounded by the ready timeout.
func (s *Sensor) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.dev.Write(ctx, regCtrl1, ctrl1BDU); err != nil {
		return s.fail("close", regCtrl1, err)
	}
	return nil
}

// dataReady reports whether both the pressure and the temperature of the
// conversion are ready.
func (s *Sensor) dataReady(ctx context.Context) (bool, error) {
	st, err := s.dev.Read(ctx, regStatus)
	if err != nil {
		return false, err
	}
	return st&(statusPDA|statusTDA) == statusPDA|statusTDA, nil
}

// sample converts the output registers in s.out. pExtra and tExtra are flags the
// caller adds to those the conversion sets.
func (s *Sensor) sample(now time.Time, pExtra, tExtra vane.Flag) Sample {
	rawP := int24LE(s.out[0], s.out[1], s.out[2])
	rawT := int16LE(s.out[3], s.out[4])

	p := pressure(rawP)
	pFlags := pExtra
	if p < pressureMinOperating || p > pressureMaxOperating {
		pFlags |= vane.FlagOutOfRange
	}
	t := temperature(rawT)
	tFlags := tExtra
	if t > tempMaxOperating {
		tFlags |= vane.FlagOutOfRange
	}
	return Sample{
		Pressure:    vane.Measurement[vane.Hectopascal]{Value: vane.Hectopascal(p), Raw: rawP, Source: s.src, Time: now, Flags: pFlags},
		Temperature: vane.Measurement[vane.Celsius]{Value: vane.Celsius(t), Raw: int32(rawT), Source: s.src, Time: now, Flags: tFlags},
	}
}
