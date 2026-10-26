package tcs3400

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/internal/poll"
	"github.com/JonasBorgesLM/vane/internal/regio"
)

// Sample is one integration: the four channels, which share a Time, and the
// configuration they were taken with.
type Sample struct {
	Clear, Red, Green, Blue vane.Measurement[vane.Counts]
	Gain                    Gain
	IntegrationTime         time.Duration
}

// Variant is which pair of parts the ID register names (p. 20). The address
// tells the two parts of a pair apart; see Sensor.Part.
type Variant uint8

// The variants.
const (
	// VariantVDDBus is the TCS34001 (0x39) or TCS34005 (0x29): VID 00, I²C
	// bus at VDD.
	VariantVDDBus Variant = 0b00
	// Variant1V8Bus is the TCS34003 (0x39) or TCS34007 (0x29): VID 11, I²C
	// bus at 1.8 V.
	Variant1V8Bus Variant = 0b11
)

// Sensor is a TCS3400 on a bus. It is safe for concurrent use: a mutex makes
// each Read one sequence on the chip (ADR-0004).
type Sensor struct {
	dev     *regio.Dev
	src     vane.Source
	cfg     config
	variant Variant

	mu     sync.Mutex
	clk    poll.Clock
	ready  func(context.Context) (bool, error)
	status uint8 // the last STATUS read by ready
	cfgBuf [15]byte
	out    [8]byte
	closed bool
}

const opRead = "read"

// New identifies the chip, then configures it and leaves it asleep. Nothing is
// written before the ID register has been read and matched (RF-01).
func New(ctx context.Context, bus vane.Bus, opts ...Option) (*Sensor, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Sensor{
		dev: regio.NewByteAddressed(bus, cfg.addr),
		src: vane.Source{Chip: vane.ChipTCS3400, Bus: cfg.bus, Address: cfg.addr},
		cfg: cfg,
		clk: poll.Reusable(),
	}
	s.ready = s.cycleDone
	v, err := s.identify(ctx)
	if err != nil {
		return nil, err
	}
	s.variant = v
	if err := s.dev.Write(ctx, regEnable, enableOff); err != nil {
		return nil, s.fail("configure", regEnable, err)
	}
	if err := s.configure(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Identify reads the ID register and writes nothing, for a read-only look at a
// bus such as vane-probe's.
func Identify(ctx context.Context, bus vane.Bus, opts ...Option) error {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	s := &Sensor{dev: regio.NewByteAddressed(bus, cfg.addr), src: vane.Source{Chip: vane.ChipTCS3400, Bus: cfg.bus, Address: cfg.addr}, cfg: cfg}
	_, err := s.identify(ctx)
	return err
}

func (s *Sensor) identify(ctx context.Context) (Variant, error) {
	id, err := s.dev.Read(ctx, regID)
	if err != nil {
		return 0, s.fail("identify", regID, err)
	}
	vid := Variant(id & 0x03)
	if id>>2 != idFamily || (vid != VariantVDDBus && vid != Variant1V8Bus) {
		return 0, &vane.IdentityError{Source: s.src, Register: regID, Want: idFamily<<2 | uint8(VariantVDDBus), Got: id}
	}
	return vid, nil
}

func (s *Sensor) fail(op string, reg uint8, err error) error {
	return &vane.SensorError{Source: s.src, Op: op, Reg: reg, HasReg: true, Err: err}
}

func (s *Sensor) atime() uint8   { return uint8(256 - s.cfg.cycles) } // #nosec G115 -- cycles is 1 to 256
func (s *Sensor) control() uint8 { return gainCodes[s.cfg.gain] }

// configure writes the integration time and gain, which must be set before AEN
// (p. 15).
func (s *Sensor) configure(ctx context.Context) error {
	if err := s.dev.Write(ctx, regATime, s.atime()); err != nil {
		return s.fail("configure", regATime, err)
	}
	if err := s.dev.Write(ctx, regControl, s.control()); err != nil {
		return s.fail("configure", regControl, err)
	}
	return nil
}

// Variant reports which pair of parts the chip's ID names.
func (s *Sensor) Variant() Variant { return s.variant }

// Part names the exact part from the variant and the address, or "" for an
// address the datasheet does not give.
func (s *Sensor) Part() string {
	switch {
	case s.cfg.addr == DefaultAddress && s.variant == VariantVDDBus:
		return "TCS34001"
	case s.cfg.addr == DefaultAddress && s.variant == Variant1V8Bus:
		return "TCS34003"
	case s.cfg.addr == AltAddress && s.variant == VariantVDDBus:
		return "TCS34005"
	case s.cfg.addr == AltAddress && s.variant == Variant1V8Bus:
		return "TCS34007"
	}
	return ""
}

// Source identifies the chip in measurements and errors.
func (s *Sensor) Source() vane.Source { return s.src }

func (s *Sensor) cycleDone(ctx context.Context) (bool, error) {
	st, err := s.dev.Read(ctx, regStatus)
	if err != nil {
		return false, err
	}
	s.status = st
	return st&statusAVALID != 0, nil
}

// Read takes one integration and returns it: about the integration time, 178 ms
// by default. A cycle that does not complete within the ready timeout is
// vane.ErrNotReady.
//
// Before each integration the integration time and gain are read back, and
// written again if the chip lost them: a chip that reset would otherwise run at
// its reset values (2.78 ms, 1×) and return counts of another scale with no
// error.
func (s *Sensor) Read(ctx context.Context) (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Sample{}, &vane.SensorError{Source: s.src, Op: opRead, Err: vane.ErrClosed}
	}
	if err := s.dev.ReadBlock(ctx, regATime, s.cfgBuf[:]); err != nil {
		return Sample{}, s.fail(opRead, regATime, err)
	}
	if s.cfgBuf[0] != s.atime() || s.cfgBuf[regControl-regATime]&0x03 != s.control() {
		if err := s.configure(ctx); err != nil {
			return Sample{}, err
		}
	}
	if err := s.dev.Write(ctx, regEnable, enableOn); err != nil {
		return Sample{}, s.fail("trigger", regEnable, err)
	}
	waitErr := poll.Until(ctx, s.clk, s.cfg.pollInterval, s.cfg.timeout(), s.ready)
	if waitErr != nil {
		// Leave the chip asleep whatever happened, and report both if that fails
		// too: the wait's error stays first, and errors.Is still finds it.
		stopErr := s.dev.Write(stopContext(ctx), regEnable, enableOff)
		return Sample{}, s.fail("wait", regStatus, errors.Join(waitErr, stopErr))
	}
	now := s.cfg.now()
	readErr := s.dev.ReadBlock(ctx, regData, s.out[:])
	if err := s.dev.Write(stopContext(ctx), regEnable, enableOff); err != nil && readErr == nil {
		return Sample{}, s.fail("stop", regEnable, err)
	}
	if readErr != nil {
		return Sample{}, s.fail(opRead, regData, readErr)
	}
	var flags vane.Flag
	if s.status&statusASAT != 0 {
		flags = vane.FlagClamped // analog saturation: the chip says so (p. 20)
	}
	channel := func(i int) vane.Measurement[vane.Counts] {
		raw := uint32(s.out[2*i]) | uint32(s.out[2*i+1])<<8
		f := flags
		if raw >= s.cfg.maxCount() {
			f |= vane.FlagClamped // digital saturation: the count's ceiling
		}
		return vane.Measurement[vane.Counts]{Value: vane.Counts(raw), Raw: int32(raw), Source: s.src, Time: now, Flags: f} // #nosec G115 -- raw <= 65535
	}
	return Sample{
		Clear: channel(0), Red: channel(1), Green: channel(2), Blue: channel(3),
		Gain: s.cfg.gain, IntegrationTime: s.cfg.integration(),
	}, nil
}

// stopContext is the context for turning the ADC off after a Read: the
// caller's, unless it has ended, since the chip should sleep even then. Only
// that case pays for a new context.
func stopContext(ctx context.Context) context.Context {
	if ctx.Err() != nil {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

// Close puts the chip to sleep. It is idempotent; a Read after it returns
// vane.ErrClosed.
func (s *Sensor) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.dev.Write(ctx, regEnable, enableOff); err != nil {
		return s.fail("close", regEnable, err)
	}
	return nil
}
