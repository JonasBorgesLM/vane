package lps25h_test

import (
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

// leaveUnreadSample makes the chip convert once and leaves the result unread,
// as a process killed between trigger and read, or another tool, would. With
// BDU on, the data-ready bits stay set until the output is read (#91).
func leaveUnreadSample(t *testing.T, r *rig) {
	t.Helper()
	write := func(reg, v byte) {
		if err := r.bus.Tx(bg, 0x5C, []byte{reg, v}, nil); err != nil {
			t.Fatal(err)
		}
	}
	write(0x20, 0x84) // PD, BDU, one-shot
	write(0x21, 0x01) // ONE_SHOT
	st := make([]byte, 1)
	for range 100 {
		if err := r.bus.Tx(bg, 0x5C, []byte{0x27}, st); err != nil {
			t.Fatal(err)
		}
		if st[0]&0x03 == 0x03 {
			return
		}
	}
	t.Fatal("the setup conversion did not finish")
}

// Seen on a Pi: the first Read returned a sample taken before the chip was
// heated, 17 °C below the next one. Negative control: without the drain before
// the trigger, the first Read returns 25 °C.
func TestReadIgnoresASampleLeftByAnEarlierUser(t *testing.T) {
	r := newRig(t)
	r.model.Set(1000, 25)
	leaveUnreadSample(t, r)
	r.model.Set(1000, 40)

	s := newSensor(t, r)
	for i := range 3 {
		got, err := s.Read(bg)
		if err != nil {
			t.Fatal(err)
		}
		if got.Temperature.Value != 40 {
			t.Fatalf("read %d: temperature %v, want the 40 °C the chip measures now, not the 25 °C sample left unread", i, got.Temperature.Value)
		}
	}
	if st := r.model.Peek(0x27); st&0x03 != 0 {
		t.Errorf("STATUS_REG = %#x after the reads: a sample was left for the next user", st)
	}
}

// A ONE_SHOT written while powered down is latched and runs on power-up (#78).
// The first Read must let it finish before its own trigger: the model reports
// a trigger on top of it as two sequences on one chip. Negative control: without
// marking it dirty in takeOver, this fails with that protocol violation.
func TestALatchedOneShotFinishesBeforeTheFirstTrigger(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(3))
	if err := r.bus.Tx(bg, 0x5C, []byte{0x20, 0x04}, nil); err != nil { // BDU, powered down
		t.Fatal(err)
	}
	if err := r.bus.Tx(bg, 0x5C, []byte{0x21, 0x01}, nil); err != nil { // latched
		t.Fatal(err)
	}
	r.model.Set(1000, 40)
	s := newSensor(t, r)
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Temperature.Value != 40 {
		t.Fatalf("temperature %v, want 40", got.Temperature.Value)
	}
}
