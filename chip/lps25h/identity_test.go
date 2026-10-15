package lps25h_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/vanetest"
)

func ptr[T any](v T) *T { return &v }

func TestNewIdentifiesThenConfigures(t *testing.T) {
	r := newRig(t)
	s, err := lps25h.New(bg, r.bus, lps25h.WithBusName("/dev/i2c-1"))
	if err != nil {
		t.Fatal(err)
	}
	want := vane.Source{Chip: vane.ChipLPS25H, Bus: "/dev/i2c-1", Address: 0x5C}
	if s.Source() != want {
		t.Errorf("Source() = %+v, want %+v", s.Source(), want)
	}
	// Powered up, ODR 0 (one-shot), block data update on: PD | BDU.
	if got := r.model.Peek(0x20); got != 0x84 {
		t.Errorf("CTRL_REG1 = %#x, want 0x84", got)
	}
}

// The driver writes CTRL_REG1 and nothing else. The averaging register is left
// as the chip has it, because the sample counts per code are unconfirmed, and
// CTRL_REG2 is not cleared because a conversion may be pending in it.
func TestNewWritesCtrlReg1Only(t *testing.T) {
	r := newRig(t)
	if _, err := lps25h.New(bg, r.bus); err != nil {
		t.Fatal(err)
	}
	w := writes(r.bus)
	if len(w) != 1 || len(w[0].W) != 2 || w[0].W[0] != 0x20 || w[0].W[1] != 0x84 {
		t.Fatalf("writes:\n%s", describe(w))
	}
	if got := r.model.Peek(0x10); got != 0x05 {
		t.Errorf("RES_CONF = %#x, want it untouched at the model's 0x05", got)
	}
}

// Negative control, seen failing: calling configure before identify in New.
func TestTheIdentityRegisterIsReadBeforeAnythingIsWritten(t *testing.T) {
	r := newRig(t)
	if _, err := lps25h.New(bg, r.bus); err != nil {
		t.Fatal(err)
	}
	log := r.bus.Log()
	if len(log) == 0 || len(log[0].W) != 1 || log[0].W[0] != 0x0F || len(log[0].R) != 1 || log[0].R[0] != 0xBD {
		t.Fatalf("the first transaction is not the WHO_AM_I read:\n%s", describe(log))
	}
}

func TestAWrongChipIsRefusedAndNothingIsWritten(t *testing.T) {
	for name, id := range map[string]uint8{"an HTS221": 0xBC, "an LSM9DS1": 0x68, "all ones": 0xFF, "zero": 0x00} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, vanetest.WithWhoAmI(id))
			s, err := lps25h.New(bg, r.bus, lps25h.WithBusName("/dev/i2c-1"))
			if s != nil {
				t.Fatal("New returned a sensor for the wrong chip")
			}
			var ie *vane.IdentityError
			if !errors.As(err, &ie) || ie.Want != 0xBD || ie.Got != id || ie.Register != 0x0F {
				t.Fatalf("error = %v, want an IdentityError{Want: 0xbd, Got: %#x, Register: 0x0f}", err, id)
			}
			if !errors.Is(err, vane.ErrUnexpectedChip) {
				t.Errorf("errors.Is(%v, ErrUnexpectedChip) = false", err)
			}
			if ie.Source.Chip != vane.ChipLPS25H || ie.Source.Address != 0x5C || ie.Source.Bus != "/dev/i2c-1" {
				t.Errorf("the error does not say which device: %+v", ie.Source)
			}
			if w := writes(r.bus); len(w) != 0 {
				t.Fatalf("%d writes reached a chip that failed identification:\n%s", len(w), describe(r.bus.Log()))
			}
			if n := len(r.bus.Log()); n != 1 {
				t.Errorf("%d transactions, want only the identity read", n)
			}
		})
	}
}

// ADR-0003: the LPS25H and the LPS25HB are one family, and nothing readable
// tells them apart, so the sensor says so instead of guessing.
func TestVariantIsUnknownBecauseNothingReadableDistinguishesTheChips(t *testing.T) {
	s, err := lps25h.New(bg, newRig(t).bus)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Variant(); got != lps25h.VariantUnknown {
		t.Errorf("Variant() = %v, want VariantUnknown", got)
	}
}

func TestIdentificationFailsCleanly(t *testing.T) {
	tests := []struct {
		name  string
		fault *vanetest.Fault
		empty bool
		want  error
	}{
		{name: "nobody at the address", empty: true, want: vane.ErrNotPresent},
		{name: "a NACK", fault: ptr(vanetest.NACK()), want: vane.ErrNotPresent},
		{name: "an I/O error", fault: ptr(vanetest.EIO()), want: vanetest.ErrIO},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			var bus vane.Bus = r.bus
			if tc.empty {
				bus = vanetest.NewFakeBus()
			}
			if tc.fault != nil {
				r.bus.Inject(0x5C, *tc.fault)
			}
			_, err := lps25h.New(bg, bus)
			var se *vane.SensorError
			if !errors.As(err, &se) || se.Op != "identify" || !se.HasReg || se.Reg != 0x0F || !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want an identify SensorError at 0x0f wrapping %v", err, tc.want)
			}
			if w := writes(r.bus); len(w) != 0 {
				t.Errorf("%d writes after a failed identification", len(w))
			}
		})
	}
}

func TestAConfigurationWriteThatFailsIsReportedAsSuch(t *testing.T) {
	r := newRig(t)
	r.bus.Inject(0x5C, vanetest.EIO().After(1))
	_, err := lps25h.New(bg, r.bus)
	var se *vane.SensorError
	if !errors.As(err, &se) || se.Op != "configure" || se.Reg != 0x20 || !errors.Is(err, vanetest.ErrIO) {
		t.Fatalf("error = %v, want a configure SensorError at 0x20 wrapping ErrIO", err)
	}
}

func TestACancelledContextTouchesNothing(t *testing.T) {
	r := newRig(t)
	cctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := lps25h.New(cctx, r.bus); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if n := len(r.bus.Log()); n != 0 {
		t.Fatalf("%d transactions for a cancelled context", n)
	}
}

func TestOptionsAreValidatedBeforeTheBusIsUsed(t *testing.T) {
	r := newRig(t)
	if _, err := lps25h.New(bg, r.bus, lps25h.WithAddress(0x5F)); !errors.Is(err, vane.ErrNotPresent) {
		t.Errorf("a sensor at 0x5f on a bus with a model at 0x5c: error = %v", err)
	}
	for _, bad := range []uint16{0x80, 0x100, 0xFFFF} {
		r := newRig(t)
		if _, err := lps25h.New(bg, r.bus, lps25h.WithAddress(bad)); err == nil {
			t.Errorf("address %#x accepted", bad)
		}
		if n := len(r.bus.Log()); n != 0 {
			t.Errorf("address %#x: %d transactions before the option was refused", bad, n)
		}
	}
	if _, err := lps25h.New(bg, newRig(t).bus, lps25h.WithClock(nil)); err == nil {
		t.Error("a nil clock was accepted")
	}
}
