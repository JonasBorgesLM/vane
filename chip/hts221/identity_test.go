package hts221_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

func TestNewIdentifiesThenConfigures(t *testing.T) {
	r := newRig(t)
	s, err := hts221.New(bg, r.bus, hts221.WithBusName("/dev/i2c-1"))
	if err != nil {
		t.Fatal(err)
	}
	want := vane.Source{Chip: vane.ChipHTS221, Bus: "/dev/i2c-1", Address: 0x5F}
	if s.Source() != want {
		t.Errorf("Source() = %+v, want %+v", s.Source(), want)
	}
	// Block data update on, powered up, one-shot (ODR = 0); default averaging.
	if got := r.model.Peek(0x20); got != 0x84 {
		t.Errorf("CTRL_REG1 = %#x, want 0x84 (PD | BDU)", got)
	}
	if got := r.model.Peek(0x10); got != 0x1B {
		t.Errorf("AV_CONF = %#x, want 0x1b", got)
	}
}

// Negative control, seen failing: calling configure before identify in New
// makes this test and the one below fail.
func TestTheIdentityRegisterIsReadBeforeAnythingIsWritten(t *testing.T) {
	r := newRig(t)
	if _, err := hts221.New(bg, r.bus); err != nil {
		t.Fatal(err)
	}
	log := r.bus.Log()
	if len(log) == 0 || len(log[0].W) != 1 || log[0].W[0] != 0x0F || len(log[0].R) != 1 || log[0].R[0] != 0xBC {
		t.Fatalf("the first transaction is not the WHO_AM_I read:\n%s", describe(log))
	}
	if len(writes(r.bus)) == 0 {
		t.Fatal("the sensor was never configured")
	}
}

func TestAWrongChipIsRefusedAndNothingIsWritten(t *testing.T) {
	for name, id := range map[string]uint8{"an LPS25H": 0xBD, "all ones": 0xFF, "zero": 0x00} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, vanetest.WithWhoAmI(id))
			s, err := hts221.New(bg, r.bus, hts221.WithBusName("/dev/i2c-1"))
			if s != nil {
				t.Fatal("New returned a sensor for the wrong chip")
			}
			var ie *vane.IdentityError
			if !errors.As(err, &ie) || ie.Want != 0xBC || ie.Got != id || ie.Register != 0x0F {
				t.Fatalf("error = %v, want an IdentityError{Want: 0xbc, Got: %#x, Register: 0x0f}", err, id)
			}
			if !errors.Is(err, vane.ErrUnexpectedChip) {
				t.Errorf("errors.Is(%v, ErrUnexpectedChip) = false", err)
			}
			if ie.Source.Chip != vane.ChipHTS221 || ie.Source.Address != 0x5F || ie.Source.Bus != "/dev/i2c-1" {
				t.Errorf("the error does not say which device: %+v", ie.Source)
			}
			if w := writes(r.bus); len(w) != 0 {
				t.Fatalf("%d writes reached the bus for a chip that failed identification:\n%s", len(w), describe(r.bus.Log()))
			}
			if n := len(r.bus.Log()); n != 1 {
				t.Errorf("%d transactions, want only the identity read:\n%s", n, describe(r.bus.Log()))
			}
		})
	}
}

func TestIdentificationFailsCleanly(t *testing.T) {
	tests := []struct {
		name   string
		fault  *vanetest.Fault
		bus    func() vane.Bus
		want   error
		wantOp string
	}{
		{name: "nobody at the address", bus: func() vane.Bus { return vanetest.NewFakeBus() }, want: vane.ErrNotPresent, wantOp: "identify"},
		{name: "a NACK", fault: ptr(vanetest.NACK()), want: vane.ErrNotPresent, wantOp: "identify"},
		{name: "an I/O error", fault: ptr(vanetest.EIO()), want: vanetest.ErrIO, wantOp: "identify"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			var bus vane.Bus = r.bus
			if tc.bus != nil {
				bus = tc.bus()
			}
			if tc.fault != nil {
				r.bus.Inject(0x5F, *tc.fault)
			}
			_, err := hts221.New(bg, bus)
			var se *vane.SensorError
			if !errors.As(err, &se) || se.Op != tc.wantOp || !se.HasReg || se.Reg != 0x0F {
				t.Fatalf("error = %v, want a SensorError for op %q at register 0x0f", err, tc.wantOp)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("errors.Is(%v, %v) = false", err, tc.want)
			}
			if w := writes(r.bus); len(w) != 0 {
				t.Errorf("%d writes after a failed identification", len(w))
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestACancelledContextTouchesNothing(t *testing.T) {
	r := newRig(t)
	cctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := hts221.New(cctx, r.bus); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if n := len(r.bus.Log()); n != 0 {
		t.Fatalf("%d transactions for a cancelled context", n)
	}
}

func TestAddressOption(t *testing.T) {
	r := newRig(t)
	if _, err := hts221.New(bg, r.bus, hts221.WithAddress(0x5C)); !errors.Is(err, vane.ErrNotPresent) {
		t.Errorf("a sensor at 0x5c on a bus with a model at 0x5f: error = %v", err)
	}
	for _, bad := range []uint16{0x80, 0x100, 0xFFFF} {
		r := newRig(t)
		if _, err := hts221.New(bg, r.bus, hts221.WithAddress(bad)); err == nil {
			t.Errorf("address %#x accepted", bad)
		}
		if n := len(r.bus.Log()); n != 0 {
			t.Errorf("address %#x: %d transactions before the option was refused", bad, n)
		}
	}
	if _, err := hts221.New(bg, newRig(t).bus, hts221.WithClock(nil)); err == nil {
		t.Error("a nil clock was accepted")
	}
}
