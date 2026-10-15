package hts221_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// Negative control, seen failing: an Identify that configures the chip, as New
// does, writes to a chip a diagnostic only meant to look at.
func TestIdentifyReadsTheIdentityAndWritesNothing(t *testing.T) {
	r := newRig(t)
	before := r.model.Peek(0x20)
	if err := hts221.Identify(bg, r.bus, hts221.WithBusName("/dev/i2c-1")); err != nil {
		t.Fatal(err)
	}
	log := r.bus.Log()
	if len(log) != 1 || len(log[0].W) != 1 || log[0].W[0] != 0x0F || len(log[0].R) != 1 || log[0].R[0] != 0xBC {
		t.Fatalf("Identify made these transactions, want only the WHO_AM_I read:\n%s", describe(log))
	}
	if w := writes(r.bus); len(w) != 0 {
		t.Errorf("%d writes", len(w))
	}
	if got := r.model.Peek(0x20); got != before {
		t.Errorf("CTRL_REG1 went from %#x to %#x: the chip's state was changed", before, got)
	}
}

func TestIdentifyReportsWhatNewWould(t *testing.T) {
	r := newRig(t, vanetest.WithWhoAmI(0xBD))
	err := hts221.Identify(bg, r.bus, hts221.WithBusName("/dev/i2c-1"))
	var ie *vane.IdentityError
	if !errors.As(err, &ie) || ie.Got != 0xBD || ie.Want != 0xBC || !errors.Is(err, vane.ErrUnexpectedChip) {
		t.Errorf("error = %v, want an IdentityError{Want 0xbc, Got 0xbd}", err)
	}

	err = hts221.Identify(bg, vanetest.NewFakeBus())
	var se *vane.SensorError
	if !errors.Is(err, vane.ErrNotPresent) || !errors.As(err, &se) || se.Op != "identify" {
		t.Errorf("an empty bus: error = %v, want an identify SensorError wrapping ErrNotPresent", err)
	}
}

func TestIdentifyValidatesItsOptionsAndHonoursTheContext(t *testing.T) {
	r := newRig(t)
	if err := hts221.Identify(bg, r.bus, hts221.WithAddress(0x80)); err == nil {
		t.Error("an address that is not 7-bit was accepted")
	}
	cctx, cancel := context.WithCancel(bg)
	cancel()
	if err := hts221.Identify(cctx, r.bus); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if n := len(r.bus.Log()); n != 0 {
		t.Errorf("%d transactions for refused calls", n)
	}
}
