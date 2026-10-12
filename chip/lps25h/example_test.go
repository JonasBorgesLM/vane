package lps25h_test

import (
	"context"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// A one-shot read against the register model. On a Pi the bus is an
// i2cdev.Bus; nothing else changes.
func ExampleSensor_Read() {
	ctx := context.Background()
	chip := vanetest.NewLPS25H()
	chip.Set(1013.25, 22.5)

	s, err := lps25h.New(ctx, vanetest.NewFakeBus(chip),
		lps25h.WithBusName("/dev/i2c-1"),
		lps25h.WithClock(func() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }))
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
	fmt.Printf("%.2f hPa (raw %d), %.1f °C (raw %d)\n",
		float64(sample.Pressure.Value), sample.Pressure.Raw,
		float64(sample.Temperature.Value), sample.Temperature.Raw)
	fmt.Println(sample.Pressure.Source, sample.Pressure.Time.Format(time.RFC3339))
	// Output:
	// 1013.25 hPa (raw 4150272), 22.5 °C (raw -9600)
	// LPS25H@0x5c on /dev/i2c-1 2026-10-20T10:00:00Z
}

// In continuous mode a read that finds nothing newer than the last one says so
// with a flag instead of an error, and a sample the chip says it overwrote
// carries its own flag.
func ExampleContinuous() {
	ctx := context.Background()
	chip := vanetest.NewLPS25H()
	chip.Set(1013.25, 22.5)

	s, err := lps25h.New(ctx, vanetest.NewFakeBus(chip), lps25h.WithMode(lps25h.Continuous(lps25h.Rate25Hz)))
	if err != nil {
		fmt.Println(err)
		return
	}
	chip.NewSample() // the chip finishes a conversion on its own, at 25 Hz

	for range 2 {
		sample, _ := s.Read(ctx)
		fmt.Printf("%.2f hPa, flags %q\n", float64(sample.Pressure.Value), sample.Pressure.Flags)
	}
	// Output:
	// 1013.25 hPa, flags ""
	// 1013.25 hPa, flags "stale"
}

// The LPS25H and the LPS25HB cannot be told apart by anything readable, so a
// Sensor says so.
func ExampleSensor_Variant() {
	s, err := lps25h.New(context.Background(), vanetest.NewFakeBus(vanetest.NewLPS25H()))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(s.Variant() == lps25h.VariantUnknown)
	// Output: true
}
