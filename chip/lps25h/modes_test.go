package lps25h_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// Status register bits, repeated from the sources in docs/hardware.md.
const (
	tDA = 1 << 0
	pDA = 1 << 1
	tOR = 1 << 4
	pOR = 1 << 5
)

func mustRead(t *testing.T, s *lps25h.Sensor) lps25h.Sample {
	t.Helper()
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// ctrl1Writes returns the values written to CTRL_REG1, in order.
func ctrl1Writes(bus *vanetest.FakeBus) []byte {
	var out []byte
	for _, tx := range writes(bus) {
		if len(tx.W) == 2 && tx.W[0] == 0x20 {
			out = append(out, tx.W[1])
		}
	}
	return out
}

func TestTheModeSelectsODRInBits6To4(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode lps25h.Mode
		want byte
	}{
		{"one-shot is the default", lps25h.OneShot(), 0x84},
		{"1 Hz", lps25h.Continuous(lps25h.Rate1Hz), 0x94},
		{"7 Hz", lps25h.Continuous(lps25h.Rate7Hz), 0xA4},
		{"12.5 Hz", lps25h.Continuous(lps25h.Rate12Hz5), 0xB4},
		{"25 Hz", lps25h.Continuous(lps25h.Rate25Hz), 0xC4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			newSensor(t, r, lps25h.WithMode(tc.mode))
			if got := r.model.Peek(0x20); got != tc.want {
				t.Errorf("CTRL_REG1 = %#x, want %#x (PD | BDU | ODR << 4)", got, tc.want)
			}
		})
	}
	r := newRig(t)
	newSensor(t, r)
	if got := r.model.Peek(0x20); got != 0x84 {
		t.Errorf("no WithMode: CTRL_REG1 = %#x, want one-shot 0x84", got)
	}
}

func TestAnInvalidConfigurationIsRefusedBeforeTheBusIsUsed(t *testing.T) {
	for name, opt := range map[string]lps25h.Option{
		"rate 0":           lps25h.WithMode(lps25h.Continuous(0)),
		"rate 5":           lps25h.WithMode(lps25h.Continuous(5)),
		"rate 255":         lps25h.WithMode(lps25h.Continuous(255)),
		"poll interval 0":  lps25h.WithPollInterval(0),
		"negative timeout": lps25h.WithReadyTimeout(-time.Second),
	} {
		r := newRig(t)
		if _, err := lps25h.New(bg, r.bus, opt); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if n := len(r.bus.Log()); n != 0 {
			t.Errorf("%s: %d transactions before the option was refused", name, n)
		}
	}
}

// Negative control, seen failing: a configure that leaves BDU out for one mode,
// or a Close that writes 0. The model also reports any such write as a
// violation to the test.
func TestBDUIsOnInEveryWriteOfEveryPath(t *testing.T) {
	scenarios := map[string]func(t *testing.T, r *rig){
		"one-shot": func(t *testing.T, r *rig) {
			s := newSensor(t, r)
			mustRead(t, s)
			_ = s.Close(bg)
		},
		"continuous 1 Hz":    func(t *testing.T, r *rig) { newSensor(t, r, lps25h.WithMode(lps25h.Continuous(lps25h.Rate1Hz))) },
		"continuous 7 Hz":    func(t *testing.T, r *rig) { newSensor(t, r, lps25h.WithMode(lps25h.Continuous(lps25h.Rate7Hz))) },
		"continuous 12.5 Hz": func(t *testing.T, r *rig) { newSensor(t, r, lps25h.WithMode(lps25h.Continuous(lps25h.Rate12Hz5))) },
		"continuous 25 Hz":   func(t *testing.T, r *rig) { newSensor(t, r, lps25h.WithMode(lps25h.Continuous(lps25h.Rate25Hz))) },
		"after a reset": func(t *testing.T, r *rig) {
			s := newSensor(t, r, lps25h.WithReadyTimeout(20*time.Millisecond))
			r.model.Reset()
			if _, err := s.Read(bg); !errors.Is(err, vane.ErrNotReady) {
				t.Fatalf("read after a reset: %v", err)
			}
			mustRead(t, s)
		},
		"close": func(t *testing.T, r *rig) {
			s := newSensor(t, r)
			if err := s.Close(bg); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			run(t, r)
			got := ctrl1Writes(r.bus)
			if len(got) == 0 {
				t.Fatal("CTRL_REG1 was never written")
			}
			for i, v := range got {
				if v&0x04 == 0 {
					t.Errorf("write %d of CTRL_REG1 = %#x, BDU is off", i, v)
				}
				if v&0x0B != 0 {
					t.Errorf("write %d of CTRL_REG1 = %#x sets SIM, RESET_AZ or DIFF_EN", i, v)
				}
			}
		})
	}
}

func continuous(t *testing.T) (*rig, *lps25h.Sensor) {
	t.Helper()
	r := newRig(t)
	return r, newSensor(t, r, lps25h.WithMode(lps25h.Continuous(lps25h.Rate7Hz)))
}

// FlagStale, one quantity at a time. Each data-ready bit is the chip's way of
// saying that quantity has a sample newer than the last one read.
func TestContinuousFlagsEachQuantityStaleWhenItsDataReadyBitIsClear(t *testing.T) {
	r, s := continuous(t)
	r.model.Set(1000, 30)

	first := mustRead(t, s) // nothing has been sampled yet
	if !first.Pressure.Flags.Has(vane.FlagStale) || !first.Temperature.Flags.Has(vane.FlagStale) {
		t.Errorf("a read before the first sample: flags %q, %q, want both stale", first.Pressure.Flags, first.Temperature.Flags)
	}

	r.model.NewSample()
	fresh := mustRead(t, s)
	if fresh.Pressure.Flags != 0 || fresh.Temperature.Flags != 0 {
		t.Errorf("a new sample is flagged %q, %q", fresh.Pressure.Flags, fresh.Temperature.Flags)
	}
	if fresh.Pressure.Raw != 4096000 {
		t.Errorf("pressure Raw = %d, want 4096000", fresh.Pressure.Raw)
	}

	again := mustRead(t, s)
	if !again.Pressure.Flags.Has(vane.FlagStale) || !again.Temperature.Flags.Has(vane.FlagStale) {
		t.Errorf("a repeated read: flags %q, %q, want both stale", again.Pressure.Flags, again.Temperature.Flags)
	}
	if again.Pressure.Value != fresh.Pressure.Value {
		t.Errorf("a stale read changed the value: %v then %v", fresh.Pressure.Value, again.Pressure.Value)
	}

	// Only one quantity without a new sample.
	r.model.NewSample()
	r.model.Poke(0x27, r.model.Peek(0x27)&^pDA) // pressure not ready, temperature is
	got := mustRead(t, s)
	if !got.Pressure.Flags.Has(vane.FlagStale) || got.Temperature.Flags.Has(vane.FlagStale) {
		t.Errorf("only the pressure lacks a sample: flags %q, %q", got.Pressure.Flags, got.Temperature.Flags)
	}
	r.model.NewSample()
	r.model.Poke(0x27, r.model.Peek(0x27)&^tDA)
	got = mustRead(t, s)
	if got.Pressure.Flags.Has(vane.FlagStale) || !got.Temperature.Flags.Has(vane.FlagStale) {
		t.Errorf("only the temperature lacks a sample: flags %q, %q", got.Pressure.Flags, got.Temperature.Flags)
	}
}

// FlagOverrun, one quantity at a time. The overrun bits are the chip's to
// raise; the test raises them in the model and checks the mapping.
func TestContinuousFlagsOverrunPerQuantity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		bits         byte
		wantP, wantT bool
	}{
		{"pressure overwritten", pOR, true, false},
		{"temperature overwritten", tOR, false, true},
		{"both overwritten", pOR | tOR, true, true},
		{"neither", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s := continuous(t)
			r.model.Set(1000, 30)
			r.model.NewSample()
			r.model.Poke(0x27, r.model.Peek(0x27)|tc.bits)
			got := mustRead(t, s)
			if got.Pressure.Flags.Has(vane.FlagOverrun) != tc.wantP || got.Temperature.Flags.Has(vane.FlagOverrun) != tc.wantT {
				t.Errorf("overrun flags = %q, %q; want pressure %v, temperature %v", got.Pressure.Flags, got.Temperature.Flags, tc.wantP, tc.wantT)
			}
			if got.Pressure.Flags.Has(vane.FlagStale) || got.Temperature.Flags.Has(vane.FlagStale) {
				t.Errorf("an overrun sample is fresh, not stale: %q, %q", got.Pressure.Flags, got.Temperature.Flags)
			}
		})
	}
}

// The overrun bit belongs to continuous mode: a one-shot sample is converted
// once and cannot have been overwritten.
func TestOneShotNeverReportsOverrunOrStale(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	got := mustRead(t, s)
	if got.Pressure.Flags.Has(vane.FlagOverrun|vane.FlagStale) || got.Temperature.Flags.Has(vane.FlagOverrun|vane.FlagStale) {
		t.Errorf("one-shot flags %q, %q", got.Pressure.Flags, got.Temperature.Flags)
	}
}

// FlagClamped is for a value forced into a physical range. Nothing here is: the
// output covers -2048 to 2048 hPa and -25.8 to 110.8 degC, wider than anything
// the chip measures, so the flag must never appear however extreme the input.
func TestClampedIsNeverSet(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	for _, tc := range []struct{ p, t float64 }{{-3000, -100}, {0, 0}, {5000, 300}, {1013.25, 22.5}} {
		r.model.Set(vanetest.LPS25HPressure(tc.p), vanetest.LPS25HTemperature(tc.t))
		got := mustRead(t, s)
		if got.Pressure.Flags.Has(vane.FlagClamped) || got.Temperature.Flags.Has(vane.FlagClamped) {
			t.Errorf("Set(%v, %v): flags %q, %q include clamped", tc.p, tc.t, got.Pressure.Flags, got.Temperature.Flags)
		}
	}
}

func TestContinuousModeWritesNothingAfterConfiguration(t *testing.T) {
	r, s := continuous(t)
	before := len(r.bus.Log())
	r.model.NewSample()
	mustRead(t, s)
	mustRead(t, s)
	for _, tx := range r.bus.Log()[before:] {
		if len(tx.R) == 0 {
			t.Errorf("continuous Read wrote to the chip: %x", tx.W)
		}
	}
}

func TestAResetInContinuousModeShowsAsPermanentlyStaleReads(t *testing.T) {
	r, s := continuous(t)
	r.model.Set(1000, 30)
	r.model.NewSample()
	if mustRead(t, s).Pressure.Flags.Has(vane.FlagStale) {
		t.Fatal("the first sample is stale")
	}
	r.model.Reset()
	for range 5 {
		r.model.NewSample() // a powered-down chip ignores it
		if got := mustRead(t, s); !got.Pressure.Flags.Has(vane.FlagStale) {
			t.Fatalf("a read after a reset is not stale: %q", got.Pressure.Flags)
		}
	}
}

func TestCloseCutsPowerAndIsIdempotent(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	mustRead(t, s)
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if got := r.model.Peek(0x20); got != 0x04 {
		t.Errorf("CTRL_REG1 after Close = %#x, want 0x04: power-down with BDU still on", got)
	}
	n := len(r.bus.Log())
	for range 3 {
		if err := s.Close(bg); err != nil {
			t.Errorf("a repeated Close returned %v", err)
		}
	}
	if len(r.bus.Log()) != n {
		t.Errorf("a repeated Close touched the bus")
	}
}

func TestUseAfterCloseIsErrClosedAndTouchesNothing(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	_ = s.Close(bg)
	n := len(r.bus.Log())
	_, err := s.Read(bg)
	var se *vane.SensorError
	if !errors.Is(err, vane.ErrClosed) || !errors.As(err, &se) || se.Op != "read" || se.Source.Address != 0x5C {
		t.Fatalf("error = %v, want a read SensorError wrapping ErrClosed", err)
	}
	if len(r.bus.Log()) != n {
		t.Error("a Read after Close used the bus")
	}
}

func TestACloseThatFailsStillClosesAndReportsOnce(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.bus.Inject(0x5C, vanetest.EIO().Times(1))
	err := s.Close(bg)
	var se *vane.SensorError
	if !errors.Is(err, vanetest.ErrIO) || !errors.As(err, &se) || se.Op != "close" || se.Reg != 0x20 {
		t.Fatalf("Close error = %v, want a close SensorError at 0x20 wrapping ErrIO", err)
	}
	if err := s.Close(bg); err != nil {
		t.Errorf("the second Close returned %v, want nil", err)
	}
	if _, err := s.Read(bg); !errors.Is(err, vane.ErrClosed) {
		t.Errorf("Read after a failed Close = %v, want ErrClosed", err)
	}
}

// RF-08 and G-11, in one-shot mode. A chip that browned out wakes in power-down
// and ignores ONE_SHOT, so the conversion never finishes. Read reports that and
// does not retry; the next Read finds the chip again, configures it, and works.
// Negative control, seen failing: not setting reinit on ErrNotReady.
func TestAResetIsRecoveredFromOnTheNextRead(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, lps25h.WithReadyTimeout(20*time.Millisecond))
	r.model.Set(1013.25, 22.5)
	mustRead(t, s)

	r.bus.Inject(0x5C, vanetest.Reset()) // fires inside the next transaction, before the trigger lands
	before := len(r.bus.Log())
	_, err := s.Read(bg)
	var se *vane.SensorError
	if !errors.Is(err, vane.ErrNotReady) || !errors.As(err, &se) || se.Op != "wait" {
		t.Fatalf("read on a reset chip = %v, want a wait SensorError wrapping ErrNotReady", err)
	}
	triggers := 0
	for _, tx := range r.bus.Log()[before:] {
		if len(tx.W) == 2 && tx.W[0] == 0x21 {
			triggers++
		}
	}
	if triggers != 1 {
		t.Errorf("the failing Read triggered %d times; it must not retry", triggers)
	}
	if r.model.Peek(0x20) != 0 {
		t.Fatalf("test setup: the chip was not reset (CTRL_REG1 = %#x)", r.model.Peek(0x20))
	}

	before = len(r.bus.Log())
	got := mustRead(t, s)
	if got.Pressure.Raw != 4150272 || got.Temperature.Raw != -9600 {
		t.Errorf("the recovered read = %d, %d, want 4150272, -9600", got.Pressure.Raw, got.Temperature.Raw)
	}
	log := r.bus.Log()[before:]
	if len(log) < 3 || len(log[0].W) != 1 || log[0].W[0] != 0x0F {
		t.Errorf("recovery did not start by identifying the chip:\n%s", describe(log))
	}
	if r.model.Peek(0x20) != 0x84 {
		t.Errorf("CTRL_REG1 after recovery = %#x, want 0x84", r.model.Peek(0x20))
	}
	mustRead(t, s)
}

// A chip that stalled, as opposed to one that reset, still has its conversion
// pending. The driver finds that out from ONE_SHOT and waits for it instead of
// triggering another on top of it. Negative control, seen failing: clearing
// dirty on recovery without asking the chip.
func TestAChipThatStaysStalledKeepsReportingNotReady(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, lps25h.WithReadyTimeout(15*time.Millisecond))
	r.model.StallReady(true)
	for i := range 3 {
		if _, err := s.Read(bg); !errors.Is(err, vane.ErrNotReady) {
			t.Fatalf("read %d: %v, want ErrNotReady", i, err)
		}
	}
	r.model.StallReady(false)
	if got := mustRead(t, s); got.Pressure.Raw != 0 || got.Temperature.Raw != -20400 {
		// the model holds 0 hPa and 0 degC: 0 counts and (0 - 42.5) * 480 = -20400
		t.Errorf("the read after the stall ended = %d, %d", got.Pressure.Raw, got.Temperature.Raw)
	}
}

// Negative control, seen failing: without letting the pending conversion end
// before it triggers again, the second Read puts a trigger on top of one still
// running, which the model reports as overlapping sequences.
func TestAReadCutShortDoesNotLeaveASecondTriggerOnTopOfTheFirst(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, lps25h.WithReadyTimeout(time.Hour))
	r.model.Set(1100, 60)
	r.model.StallReady(true)

	cctx, cancel := context.WithTimeout(bg, 15*time.Millisecond)
	defer cancel()
	if _, err := s.Read(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cut-short read = %v, want DeadlineExceeded", err)
	}
	r.model.StallReady(false) // the first conversion can finish now

	got := mustRead(t, s)
	// 1100 hPa is 4505600 counts; 60 degC is 17.5 above the offset, 8400 counts.
	if got.Pressure.Raw != 4505600 || got.Temperature.Raw != 8400 {
		t.Errorf("Raw = %d, %d after recovering from a cut-short read, want 4505600, 8400", got.Pressure.Raw, got.Temperature.Raw)
	}
}

// RNF-04: trigger, wait and read are one sequence per chip. Without the sensor's
// mutex, goroutines interleave their sequences and the model reports it.
// Negative control, seen failing: removing the lock in Read.
func TestConcurrentReadsDoNotInterleaveTheirSequences(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(2))
	s := newSensor(t, r)
	r.model.Set(1013.25, 22.5)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 15 {
				got, err := s.Read(bg)
				if err != nil {
					t.Error(err)
					return
				}
				if got.Pressure.Raw != 4150272 || got.Temperature.Raw != -9600 {
					t.Errorf("a concurrent read got %d, %d", got.Pressure.Raw, got.Temperature.Raw)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestReadAndCloseRace(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(1))
	s := newSensor(t, r)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if _, err := s.Read(bg); err != nil && !errors.Is(err, vane.ErrClosed) {
					t.Error(err)
					return
				}
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.Close(bg); err != nil {
		t.Error(err)
	}
	wg.Wait()
}
