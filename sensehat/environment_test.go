package sensehat_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/sensehat"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// RF-05 at read time, where the issue wants it demonstrated: a chip that opened
// fine and then fails leaves the other chip's reading valid and the call
// without an error. Negative control, seen failing: returning an error whenever
// any reading failed.
func TestAChipFailingDuringARead(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	b.bus.Inject(0x5C, vanetest.EIO())

	env, err := d.Environment(bg)
	if err != nil {
		t.Fatalf("one chip failing is not a top-level error: %v", err)
	}
	if env.Humidity.Err != nil || env.Humidity.Raw != 3500 {
		t.Errorf("the healthy chip's humidity = %+v", env.Humidity)
	}
	var se *vane.SensorError
	if !errors.Is(env.Pressure.Err, vanetest.ErrIO) || !errors.As(env.Pressure.Err, &se) || se.Source.Chip != vane.ChipLPS25H {
		t.Errorf("Pressure.Err = %v, want a SensorError for the LPS25H wrapping the I/O error", env.Pressure.Err)
	}
	if env.Temperatures[0].Err != nil || env.Temperatures[1].Err == nil {
		t.Errorf("temperatures = %+v, want the HTS221's valid and the LPS25H's failed", env.Temperatures)
	}
	// The state Open reported does not change: a failed read is that read's error.
	if states(d)[vane.ChipLPS25H] != sensehat.StateReady {
		t.Error("a failed read changed the sensor's state")
	}
	// And it recovers by itself once the fault is gone.
	b.bus.Clear(0x5C)
	if env, err := d.Environment(bg); err != nil || env.Pressure.Err != nil || env.Pressure.Raw != 4150272 {
		t.Errorf("after the fault cleared: %+v, %v", env.Pressure, err)
	}
}

func TestEveryChipFailingDuringARead(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	b.bus.Inject(0x5F, vanetest.EIO())
	b.bus.Inject(0x5C, vanetest.NACK())

	env, err := d.Environment(bg)
	if err == nil {
		t.Fatal("every chip failed and the call reported nothing")
	}
	if !errors.Is(err, vanetest.ErrIO) || !errors.Is(err, vane.ErrNotPresent) {
		t.Errorf("error = %v, want both chips' reasons joined", err)
	}
	if env.Humidity.Err == nil || env.Pressure.Err == nil || len(env.Temperatures) != 2 {
		t.Errorf("the readings should still carry their own errors: %+v", env)
	}
}

func TestACancelledContextEndsTheCall(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	before := len(b.bus.Log())
	cctx, cancel := context.WithCancel(bg)
	cancel()
	env, err := d.Environment(cctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if env.Humidity.Err == nil || env.Pressure.Err == nil {
		t.Errorf("readings after a cancelled context should carry an error: %+v", env)
	}
	if len(b.bus.Log()) != before {
		t.Errorf("%d transactions for a call whose context had ended", len(b.bus.Log())-before)
	}
}

func TestEveryEnvironmentIsAFreshReading(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	first, _ := d.Environment(bg)
	b.hts.Set(80, 30)
	b.lps.Set(900, 10)
	second, err := d.Environment(bg)
	if err != nil {
		t.Fatal(err)
	}
	if second.Humidity.Raw == first.Humidity.Raw || second.Pressure.Raw == first.Pressure.Raw {
		t.Errorf("the second call returned the first call's values: %+v", second)
	}
	if len(first.Temperatures) != 2 {
		t.Errorf("the first result changed: %+v", first.Temperatures)
	}
}

// RNF-04. Each driver serialises its own sequence, and the models report any
// that interleave, so a facade that bypassed that would fail here.
func TestConcurrentReadsAreSafeAndConsistent(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				env, err := d.Environment(bg)
				if err != nil || env.Humidity.Raw != 3500 || env.Pressure.Raw != 4150272 {
					t.Errorf("a concurrent read got %+v, %v", env, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestReadsRaceWithClose(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if _, err := d.Environment(bg); err != nil && !errors.Is(err, vane.ErrClosed) {
					t.Errorf("error = %v, want none or ErrClosed", err)
					return
				}
			}
		}()
	}
	time.Sleep(3 * time.Millisecond)
	if err := d.Close(); err != nil {
		t.Error(err)
	}
	wg.Wait()
}

func TestCloseCutsPowerToEveryChipAndIsIdempotent(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	if _, err := d.Environment(bg); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]byte{"HTS221": b.hts.Peek(0x20), "LPS25H": b.lps.Peek(0x20)} {
		if got != 0x04 {
			t.Errorf("%s CTRL_REG1 after Close = %#x, want 0x04: power-down with BDU still on", name, got)
		}
	}
	n := len(b.bus.Log())
	for range 3 {
		if err := d.Close(); err != nil {
			t.Errorf("a repeated Close returned %v", err)
		}
	}
	if len(b.bus.Log()) != n {
		t.Error("a repeated Close touched the bus")
	}
	env, err := d.Environment(bg)
	if !errors.Is(err, vane.ErrClosed) || len(b.bus.Log()) != n {
		t.Errorf("Environment after Close = %v, %d new transactions; want ErrClosed and none", err, len(b.bus.Log())-n)
	}
	if env.Humidity.Raw != 0 || len(env.Temperatures) != 0 {
		t.Errorf("a closed device returned data: %+v", env)
	}
}

func TestCloseOnlyPowersDownWhatWasOpened(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	d := b.mustOpen()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if b.touched(0x5C) {
		t.Error("Close wrote to a chip the kernel owns")
	}
}

// closeCounter is a caller-owned bus that counts Close calls.
type closeCounter struct {
	vane.Bus
	closes int
}

func (c *closeCounter) Close() error { c.closes++; return nil }

// The caller of WithBus owns the bus: Close must leave it open.
func TestCloseDoesNotCloseABusItDidNotOpen(t *testing.T) {
	b := newBoard(t)
	owned := &closeCounter{Bus: b.bus}
	d, err := sensehat.Open(bg, sensehat.WithBus(owned), sensehat.WithRoot(b.root), sensehat.WithClock(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if owned.closes != 0 {
		t.Errorf("Close closed the caller's bus %d times", owned.closes)
	}
}

func TestACloseThatFailsOnOneChipStillPowersDownTheOther(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	b.bus.Inject(0x5F, vanetest.EIO())
	err := d.Close()
	var se *vane.SensorError
	if !errors.Is(err, vanetest.ErrIO) || !errors.As(err, &se) || se.Op != "close" {
		t.Fatalf("Close = %v, want the HTS221's close error", err)
	}
	if got := b.lps.Peek(0x20); got != 0x04 {
		t.Errorf("the other chip was left with CTRL_REG1 = %#x", got)
	}
	if err := d.Close(); err != nil {
		t.Errorf("the second Close returned %v, want nil", err)
	}
}

// The close timeout bounds the power-down transaction. Negative control, seen
// failing: ignoring it leaves Close waiting out an hour-long bus delay.
func TestTheCloseTimeoutBoundsTheTransaction(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen(sensehat.WithCloseTimeout(40 * time.Millisecond))
	b.bus.Inject(0x5F, vanetest.Latency(time.Hour))
	start := time.Now()
	err := d.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Close took %v with a 40ms timeout", elapsed)
	}
	if got := b.lps.Peek(0x20); got != 0x04 {
		t.Errorf("the chip after the slow one was left with CTRL_REG1 = %#x", got)
	}
}
