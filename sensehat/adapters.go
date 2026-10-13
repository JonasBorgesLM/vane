package sensehat

import (
	"context"
	"errors"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
)

// ErrNotRequested is the error of a reading from a chip that Open was told not
// to use.
var ErrNotRequested = errors.New("sensehat: the chip was not requested")

// probe is one row of the table of what is on the board: the chip, its fixed
// address, and how to build the adapter that drives it. The table is a plain
// value and nothing registers into it, so the set of chips is readable in one
// place and does not depend on which packages happen to be imported.
type probe struct {
	chip vane.Chip
	addr uint16
	new  func(src vane.Source) adapter
}

var probeTable = []probe{
	{vane.ChipHTS221, hts221.DefaultAddress, func(src vane.Source) adapter { return &htsAdapter{src: src} }},
	{vane.ChipLPS25H, lps25h.DefaultAddress, func(src vane.Source) adapter { return &lpsAdapter{src: src} }},
}

// adapter drives one chip for the facade. It exists because each chip fills
// different fields of an Environment, and the types of those fields differ.
// It is unexported: with two chips there is nothing to gain from letting a
// caller supply a third, and exporting it would freeze a shape before the
// backend that might need a different one exists.
type adapter interface {
	// open builds the driver. It is the first traffic to the chip.
	open(ctx context.Context, bus vane.Bus, c *config) error
	// identify only reads the chip's identity and changes nothing. Diagnose uses it.
	identify(ctx context.Context, bus vane.Bus, c *config) error
	// fill sets this chip's readings in env. If err is not nil the readings carry
	// it and nothing is read; otherwise the chip is read. It returns the error the
	// readings carry, or nil if the chip delivered.
	fill(ctx context.Context, env *Environment, err error) error
	// absent marks the chip's primary reading as not requested.
	absent(env *Environment)
	// close powers the chip down. It does nothing if the chip was never opened.
	close(ctx context.Context) error
}

// driverOptions returns the options for a driver: the facade's own first, then
// the caller's, then the address last so that it always wins.
func driverOptions[O any](own, user []O, addr O) []O {
	out := make([]O, 0, len(own)+len(user)+1)
	out = append(out, own...)
	out = append(out, user...)
	return append(out, addr)
}

func readErr(src vane.Source, err error) error {
	return &vane.SensorError{Source: src, Op: "read", Err: err}
}

type htsAdapter struct {
	src vane.Source
	s   *hts221.Sensor
}

func (a *htsAdapter) open(ctx context.Context, bus vane.Bus, c *config) (err error) {
	a.s, err = hts221.New(ctx, bus, driverOptions(
		[]hts221.Option{hts221.WithBusName(a.src.Bus), hts221.WithClock(c.now)},
		c.hts,
		hts221.WithAddress(a.src.Address))...)
	return err
}

func (a *htsAdapter) identify(ctx context.Context, bus vane.Bus, c *config) error {
	return hts221.Identify(ctx, bus, driverOptions(
		[]hts221.Option{hts221.WithBusName(a.src.Bus)},
		c.hts,
		hts221.WithAddress(a.src.Address))...)
}

func (a *htsAdapter) fill(ctx context.Context, env *Environment, err error) error {
	if err == nil {
		sample, rerr := a.s.Read(ctx)
		if rerr == nil {
			env.Humidity = Reading[vane.RelativeHumidity]{Measurement: sample.Humidity}
			env.Temperatures = append(env.Temperatures, Reading[vane.Celsius]{Measurement: sample.Temperature})
			return nil
		}
		err = rerr
	}
	env.Humidity = Reading[vane.RelativeHumidity]{Measurement: vane.Measurement[vane.RelativeHumidity]{Source: a.src}, Err: err}
	env.Temperatures = append(env.Temperatures, Reading[vane.Celsius]{Measurement: vane.Measurement[vane.Celsius]{Source: a.src}, Err: err})
	return err
}

func (a *htsAdapter) absent(env *Environment) {
	env.Humidity = Reading[vane.RelativeHumidity]{Measurement: vane.Measurement[vane.RelativeHumidity]{Source: a.src}, Err: readErr(a.src, ErrNotRequested)}
}

func (a *htsAdapter) close(ctx context.Context) error {
	if a.s == nil {
		return nil
	}
	return a.s.Close(ctx)
}

type lpsAdapter struct {
	src vane.Source
	s   *lps25h.Sensor
}

func (a *lpsAdapter) open(ctx context.Context, bus vane.Bus, c *config) (err error) {
	a.s, err = lps25h.New(ctx, bus, driverOptions(
		[]lps25h.Option{lps25h.WithBusName(a.src.Bus), lps25h.WithClock(c.now)},
		c.lps,
		lps25h.WithAddress(a.src.Address))...)
	return err
}

func (a *lpsAdapter) identify(ctx context.Context, bus vane.Bus, c *config) error {
	return lps25h.Identify(ctx, bus, driverOptions(
		[]lps25h.Option{lps25h.WithBusName(a.src.Bus)},
		c.lps,
		lps25h.WithAddress(a.src.Address))...)
}

func (a *lpsAdapter) fill(ctx context.Context, env *Environment, err error) error {
	if err == nil {
		sample, rerr := a.s.Read(ctx)
		if rerr == nil {
			env.Pressure = Reading[vane.Hectopascal]{Measurement: sample.Pressure}
			env.Temperatures = append(env.Temperatures, Reading[vane.Celsius]{Measurement: sample.Temperature})
			return nil
		}
		err = rerr
	}
	env.Pressure = Reading[vane.Hectopascal]{Measurement: vane.Measurement[vane.Hectopascal]{Source: a.src}, Err: err}
	env.Temperatures = append(env.Temperatures, Reading[vane.Celsius]{Measurement: vane.Measurement[vane.Celsius]{Source: a.src}, Err: err})
	return err
}

func (a *lpsAdapter) absent(env *Environment) {
	env.Pressure = Reading[vane.Hectopascal]{Measurement: vane.Measurement[vane.Hectopascal]{Source: a.src}, Err: readErr(a.src, ErrNotRequested)}
}

func (a *lpsAdapter) close(ctx context.Context) error {
	if a.s == nil {
		return nil
	}
	return a.s.Close(ctx)
}
