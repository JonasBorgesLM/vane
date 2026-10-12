package lps25h

import (
	"context"
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

	mu    sync.Mutex
	out   [5]byte    // scratch for the output registers; guarded by mu
	clk   poll.Clock // one timer, reused by every wait; guarded by mu
	ready func(context.Context) (bool, error)
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
	s := &Sensor{
		dev: regio.New(bus, cfg.addr),
		src: vane.Source{Chip: vane.ChipLPS25H, Bus: cfg.bus, Address: cfg.addr},
		cfg: cfg,
	}
	// Built once: a method value made per call would allocate on every Read.
	s.ready = s.dataReady
	s.clk = poll.Reusable()
	if err := s.identify(ctx); err != nil {
		return nil, err
	}
	if err := s.configure(ctx); err != nil {
		return nil, err
	}
	return s, nil
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

// configure powers the chip up for one-shot acquisition with block data update
// on, in one write. BDU is on in every configuration (RF-06): without it a
// multi-byte read can combine bytes of two different samples. SIM, RESET_AZ and
// DIFF_EN stay clear; differential mode would change what the pressure
// registers mean.
func (s *Sensor) configure(ctx context.Context) error {
	if err := s.dev.Write(ctx, regCtrl1, ctrl1PD|ctrl1BDU); err != nil {
		return s.fail("configure", regCtrl1, err)
	}
	return nil
}

func (s *Sensor) fail(op string, reg uint8, err error) error {
	return &vane.SensorError{Source: s.src, Op: op, Reg: reg, HasReg: true, Err: err}
}

// Read triggers one conversion, waits for it, and returns the result. The
// sample is always new, and the chip idles between reads.
//
// Read stops waiting when ctx ends, but a transaction already on the bus cannot
// be interrupted, so cancellation takes effect after at most one transaction
// and one poll interval (RNF-05).
func (s *Sensor) Read(ctx context.Context) (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.dev.Write(ctx, regCtrl2, ctrl2OneShot); err != nil {
		return Sample{}, s.fail("trigger", regCtrl2, err)
	}
	if err := poll.Until(ctx, s.clk, s.cfg.pollInterval, s.cfg.readyTimeout, s.ready); err != nil {
		return Sample{}, s.fail("wait", regStatus, err)
	}
	now := s.cfg.now()
	if err := s.dev.ReadBlock(ctx, regOut, s.out[:]); err != nil {
		return Sample{}, s.fail("read", regOut, err)
	}
	return s.sample(now), nil
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

// sample converts the output registers in s.out.
func (s *Sensor) sample(now time.Time) Sample {
	rawP := int24LE(s.out[0], s.out[1], s.out[2])
	rawT := int16LE(s.out[3], s.out[4])

	p := pressure(rawP)
	var pFlags vane.Flag
	if p < pressureMinOperating || p > pressureMaxOperating {
		pFlags |= vane.FlagOutOfRange
	}
	t := temperature(rawT)
	var tFlags vane.Flag
	if t > tempMaxOperating {
		tFlags |= vane.FlagOutOfRange
	}
	return Sample{
		Pressure:    vane.Measurement[vane.Hectopascal]{Value: vane.Hectopascal(p), Raw: rawP, Source: s.src, Time: now, Flags: pFlags},
		Temperature: vane.Measurement[vane.Celsius]{Value: vane.Celsius(t), Raw: int32(rawT), Source: s.src, Time: now, Flags: tFlags},
	}
}
