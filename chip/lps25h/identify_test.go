package lps25h_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// Negative control, seen failing: an Identify that configures the chip, as New
// does, writes to a chip a diagnostic only meant to look at.
func TestIdentifyReadsTheIdentityAndWritesNothing(t *testing.T) {
	r := newRig(t)
	before := r.model.Peek(0x20)
	if err := lps25h.Identify(bg, r.bus, lps25h.WithBusName("/dev/i2c-1")); err != nil {
		t.Fatal(err)
	}
	log := r.bus.Log()
	if len(log) != 1 || len(log[0].W) != 1 || log[0].W[0] != 0x0F || len(log[0].R) != 1 || log[0].R[0] != 0xBD {
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
	r := newRig(t, vanetest.WithWhoAmI(0xBC))
	err := lps25h.Identify(bg, r.bus, lps25h.WithBusName("/dev/i2c-1"))
	var ie *vane.IdentityError
	if !errors.As(err, &ie) || ie.Got != 0xBC || ie.Want != 0xBD || !errors.Is(err, vane.ErrUnexpectedChip) {
		t.Errorf("error = %v, want an IdentityError{Want 0xbd, Got 0xbc}", err)
	}

	err = lps25h.Identify(bg, vanetest.NewFakeBus())
	var se *vane.SensorError
	if !errors.Is(err, vane.ErrNotPresent) || !errors.As(err, &se) || se.Op != "identify" {
		t.Errorf("an empty bus: error = %v, want an identify SensorError wrapping ErrNotPresent", err)
	}
}

func TestIdentifyValidatesItsOptionsAndHonoursTheContext(t *testing.T) {
	r := newRig(t)
	if err := lps25h.Identify(bg, r.bus, lps25h.WithAddress(0x80)); err == nil {
		t.Error("an address that is not 7-bit was accepted")
	}
	cctx, cancel := context.WithCancel(bg)
	cancel()
	if err := lps25h.Identify(cctx, r.bus); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if n := len(r.bus.Log()); n != 0 {
		t.Errorf("%d transactions for refused calls", n)
	}
}
