package sensehat_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/sensehat"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// A board where one chip is missing still gives a reading: the working sensor's
// value is valid, and the missing one's reading says why it is not. On a Pi,
// leave out WithBus and WithKernelCheck and Open uses /dev/i2c-1.
func ExampleOpen() {
	ctx := context.Background()
	hts := vanetest.NewHTS221()
	hts.Set(50, 25)
	// The LPS25H is not on this bus. The bus is a fake, so there is no sysfs node
	// for the kernel check to look at.
	dev, err := sensehat.Open(ctx, sensehat.WithBus(vanetest.NewFakeBus(hts)), sensehat.WithKernelCheck(false))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer dev.Close()

	for _, s := range dev.Sensors() {
		fmt.Printf("%s: %s\n", s.Source.Chip, s.State)
	}

	env, err := dev.Environment(ctx)
	fmt.Println("error:", err)
	if env.Humidity.Err == nil {
		fmt.Printf("humidity: %.1f %%RH\n", float64(env.Humidity.Value))
	}
	fmt.Println("pressure available:", env.Pressure.Err == nil, "- missing chip:", errors.Is(env.Pressure.Err, vane.ErrNotPresent))
	// Output:
	// HTS221: ready
	// LPS25H: failed
	// error: <nil>
	// humidity: 50.0 %RH
	// pressure available: false - missing chip: true
}
