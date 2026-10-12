package lps25h

import (
	"context"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/internal/regio"
)

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

// Sensor is an LPS25H or LPS25HB on a bus.
type Sensor struct {
	dev *regio.Dev
	src vane.Source
	cfg config
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
