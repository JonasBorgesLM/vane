package hts221

import (
	"context"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/internal/regio"
)

// Sensor is an HTS221 on a bus. It is safe for concurrent use.
type Sensor struct {
	dev *regio.Dev
	src vane.Source
	cfg config
}

// New identifies the chip, then configures it. The order is the requirement:
// nothing is written before the identity register has been read and matched
// (RF-01), because a write to the wrong device is not harmless, and the same
// bus carries the HAT's microcontroller.
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
