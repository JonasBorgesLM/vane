package hts221

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
	Humidity    vane.Measurement[vane.RelativeHumidity]
	Temperature vane.Measurement[vane.Celsius]
}

// Sensor is an HTS221 on a bus. It is safe for concurrent use: the mutex makes
// trigger, wait and read one sequence per chip (ADR-0004).
type Sensor struct {
	dev *regio.Dev
	src vane.Source
	cfg config
	cal Calibration

	mu    sync.Mutex
	out   [4]byte    // scratch for the output registers; guarded by mu
	clk   poll.Clock // one timer, reused by every wait; guarded by mu
	ready func(context.Context) (bool, error)
}

// New identifies the chip, reads and validates its calibration, and only then
// configures it. The order is the requirement: nothing is written before the
// identity register has been read and matched (RF-01), because a write to the
// wrong device is not harmless and the same bus carries the HAT's
// microcontroller; and a chip whose calibration cannot be used is never
// configured.
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
		src: vane.Source{Chip: vane.ChipHTS221, Bus: cfg.bus, Address: cfg.addr},
		cfg: cfg,
	}
	// Built once: a method value made per call would allocate on every Read.
	s.ready = s.dataReady
	s.clk = poll.Reusable()
	if err := s.identify(ctx); err != nil {
		return nil, err
	}
	if err := s.calibrate(ctx); err != nil {
		return nil, err
	}
	if err := s.configure(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Calibration returns the factory calibration read from the chip.
func (s *Sensor) Calibration() Calibration { return s.cal }

// Source describes where this sensor's measurements come from.
func (s *Sensor) Source() vane.Source { return s.src }

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

// calibrate reads registers 0x30 to 0x3F in one block, with the auto-increment
// bit that regio sets, and refuses a calibration that cannot be used.
func (s *Sensor) calibrate(ctx context.Context) error {
	var block [calibrationLen]byte
	if err := s.dev.ReadBlock(ctx, calibrationStart, block[:]); err != nil {
		return s.fail("calibrate", calibrationStart, err)
	}
	cal := parseCalibration(block[:])
	if err := cal.Validate(); err != nil {
		return s.fail("calibrate", calibrationStart, err)
	}
	s.cal = cal
	return nil
}

// configure powers the chip up for one-shot acquisition with block data update
// on. BDU is on in every configuration (RF-06): without it a two-byte read can
// combine the high byte of one sample with the low byte of the next.
func (s *Sensor) configure(ctx context.Context) error {
	if err := s.dev.Write(ctx, regAvConf, avConfDefault); err != nil {
		return s.fail("configure", regAvConf, err)
	}
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

// dataReady reports whether both the humidity and the temperature of the
// conversion are ready.
func (s *Sensor) dataReady(ctx context.Context) (bool, error) {
	st, err := s.dev.Read(ctx, regStatus)
	if err != nil {
		return false, err
	}
	return st&(statusHDA|statusTDA) == statusHDA|statusTDA, nil
}

// sample converts the output registers in s.out.
func (s *Sensor) sample(now time.Time) Sample {
	rawH := int16LE(s.out[0], s.out[1])
	rawT := int16LE(s.out[2], s.out[3])

	rh, clamped := s.cal.humidity(rawH)
	var hFlags vane.Flag
	if clamped {
		hFlags |= vane.FlagClamped
	}
	t := s.cal.temperature(rawT)
	var tFlags vane.Flag
	if t < tempMinOperating || t > tempMaxOperating {
		tFlags |= vane.FlagOutOfRange
	}
	return Sample{
		Humidity:    vane.Measurement[vane.RelativeHumidity]{Value: vane.RelativeHumidity(rh), Raw: int32(rawH), Source: s.src, Time: now, Flags: hFlags},
		Temperature: vane.Measurement[vane.Celsius]{Value: vane.Celsius(t), Raw: int32(rawT), Source: s.src, Time: now, Flags: tFlags},
	}
}
