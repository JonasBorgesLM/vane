package hts221_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// A one-shot read against the register model. On a Pi the bus is an
// i2cdev.Bus; nothing else changes.
func ExampleSensor_Read() {
	ctx := context.Background()
	chip := vanetest.NewHTS221()
	chip.Set(50, 25)

	s, err := hts221.New(ctx, vanetest.NewFakeBus(chip),
		hts221.WithBusName("/dev/i2c-1"),
		hts221.WithClock(func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer s.Close(ctx)

	sample, err := s.Read(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%.1f %%RH (raw %d), %.1f °C (raw %d)\n",
		float64(sample.Humidity.Value), sample.Humidity.Raw,
		float64(sample.Temperature.Value), sample.Temperature.Raw)
	fmt.Println(sample.Humidity.Source, sample.Humidity.Time.Format(time.RFC3339))
	// Output:
	// 50.0 %RH (raw 3500), 25.0 °C (raw 0)
	// HTS221@0x5f on /dev/i2c-1 2026-10-20T10:00:00Z
}

// In continuous mode a read that finds nothing newer than the last one says so
// with a flag instead of an error.
func ExampleContinuous() {
	ctx := context.Background()
	chip := vanetest.NewHTS221()
	chip.Set(50, 25)

	s, err := hts221.New(ctx, vanetest.NewFakeBus(chip), hts221.WithMode(hts221.Continuous(hts221.Rate7Hz)))
	if err != nil {
		fmt.Println(err)
		return
	}
	chip.NewSample() // the chip finishes a conversion on its own, at 7 Hz

	for range 2 {
		sample, _ := s.Read(ctx)
		fmt.Printf("%.1f %%RH, flags %q\n", float64(sample.Humidity.Value), sample.Humidity.Flags)
	}
	// Output:
	// 50.0 %RH, flags ""
	// 50.0 %RH, flags "stale"
}

// A calibration whose two humidity points read the same count cannot be used:
// converting with it would divide by zero.
func ExampleCalibration_Validate() {
	cal := hts221.Calibration{
		H0RH: 20, H1RH: 80, T0DegC: 20, T1DegC: 80,
		H0T0Out: 100, H1T0Out: 100, T0Out: 0, T1Out: 1,
	}
	err := cal.Validate()
	fmt.Println(errors.Is(err, vane.ErrCalibration))
	fmt.Println(err)
	// Output:
	// true
	// vane: degenerate factory calibration: both humidity points read the same count, 100
}
