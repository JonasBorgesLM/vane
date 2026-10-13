// Package conformance holds checks that a chip must pass, and that run twice: today
// against the register models in vanetest, and on a real Sense HAT with the
// hardware build tag.
//
// The models are the oracle of every driver test, and several things they assume
// are not confirmed on the chip (docs/hardware.md lists them). Running one set of
// checks against both is what finds out where a model is wrong: a check that
// passes on the model and fails on the chip is a model error, and the failure
// message says which assumption and which document to correct.
//
// The checks are an independent client. They talk to the chip by raw register
// access with the numbers repeated from the datasheet, not from the drivers or
// the models, because a check that borrows a constant from the thing it checks
// agrees with its mistakes.
//
// Two kinds of statement are made. An assertion holds for any correct chip and
// fails the test. An observation records what this chip does where the datasheet
// is not available to say what it should do, as a log line, and never fails: on a
// real board those lines are the evidence that settles an assumption.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
)

// Target is a chip, or a model of one, on a bus.
type Target struct {
	Bus vane.Bus
	// Advance lets time pass for the chip. A real target sleeps. A model has no
	// clock, so its Advance makes the chip produce the sample it would by then.
	Advance func(d time.Duration)
	// Prepare puts the chip's surroundings in a plausible state before the checks.
	// A model is told what to measure; a real chip needs nothing.
	Prepare func()
	// Timeout bounds the wait for one conversion. Zero means one second.
	Timeout time.Duration
}

func (tg Target) timeout() time.Duration {
	if tg.Timeout > 0 {
		return tg.Timeout
	}
	return time.Second
}

func (tg Target) advance(d time.Duration) {
	if tg.Advance != nil {
		tg.Advance(d)
	}
}

func (tg Target) prepare() {
	if tg.Prepare != nil {
		tg.Prepare()
	}
}

// dev is raw register access to one chip, with the auto-increment bit set on
// block reads as ST chips need.
type dev struct {
	t    testing.TB
	bus  vane.Bus
	addr uint16
}

var bg = context.Background()

func (d dev) read(reg uint8) uint8 {
	d.t.Helper()
	r := make([]byte, 1)
	if err := d.bus.Tx(bg, d.addr, []byte{reg}, r); err != nil {
		d.t.Fatalf("reading register 0x%02x of 0x%02x: %v", reg, d.addr, err)
	}
	return r[0]
}

func (d dev) block(reg uint8, n int) []byte {
	d.t.Helper()
	r := make([]byte, n)
	if err := d.bus.Tx(bg, d.addr, []byte{reg | 0x80}, r); err != nil {
		d.t.Fatalf("reading %d bytes from register 0x%02x of 0x%02x: %v", n, reg, d.addr, err)
	}
	return r
}

func (d dev) write(reg, val uint8) {
	d.t.Helper()
	if err := d.bus.Tx(bg, d.addr, []byte{reg, val}, nil); err != nil {
		d.t.Fatalf("writing 0x%02x to register 0x%02x of 0x%02x: %v", val, reg, d.addr, err)
	}
}

// waitFor polls the register until its value has every bit of mask set, letting
// time pass between reads, and reports whether it did within the target's
// timeout.
func (d dev) waitFor(tg Target, reg, mask uint8) bool {
	d.t.Helper()
	const step = 5 * time.Millisecond
	for waited := time.Duration(0); ; waited += step {
		if d.read(reg)&mask == mask {
			return true
		}
		if waited >= tg.timeout() {
			return false
		}
		tg.advance(step)
	}
}

// stays polls the register for the given time and reports whether it never had
// any bit of mask set.
func (d dev) stays(tg Target, reg, mask uint8, over time.Duration) bool {
	d.t.Helper()
	const step = 5 * time.Millisecond
	for waited := time.Duration(0); waited < over; waited += step {
		if d.read(reg)&mask != 0 {
			return false
		}
		tg.advance(step)
	}
	return true
}

// observe records what the chip did where nothing says what it should do.
func observe(t testing.TB, format string, args ...any) {
	t.Helper()
	t.Logf("OBSERVATION: "+format, args...)
}
