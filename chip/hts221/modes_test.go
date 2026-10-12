package hts221_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

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

func TestTheModeSelectsODRInCtrlReg1(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode hts221.Mode
		want byte
	}{
		{"one-shot is the default", hts221.OneShot(), 0x84},
		{"1 Hz", hts221.Continuous(hts221.Rate1Hz), 0x85},
		{"7 Hz", hts221.Continuous(hts221.Rate7Hz), 0x86},
		{"12.5 Hz", hts221.Continuous(hts221.Rate12Hz5), 0x87},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			newSensor(t, r, hts221.WithMode(tc.mode))
			if got := r.model.Peek(0x20); got != tc.want {
				t.Errorf("CTRL_REG1 = %#x, want %#x (PD | BDU | ODR)", got, tc.want)
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
	for name, opt := range map[string]hts221.Option{
		"rate 0":           hts221.WithMode(hts221.Continuous(0)),
		"rate 4":           hts221.WithMode(hts221.Continuous(4)),
		"rate 255":         hts221.WithMode(hts221.Continuous(255)),
		"humidity 5":       hts221.WithAveraging(hts221.Averaging{Humidity: 5}),
		"humidity 2":       hts221.WithAveraging(hts221.Averaging{Humidity: 2}),
		"humidity 1024":    hts221.WithAveraging(hts221.Averaging{Humidity: 1024}),
		"temperature 1":    hts221.WithAveraging(hts221.Averaging{Temperature: 1}),
		"temperature 512":  hts221.WithAveraging(hts221.Averaging{Temperature: 512}),
		"temperature 24":   hts221.WithAveraging(hts221.Averaging{Temperature: 24}),
		"poll interval 0":  hts221.WithPollInterval(0),
		"negative timeout": hts221.WithReadyTimeout(-time.Second),
	} {
		r := newRig(t)
		if _, err := hts221.New(bg, r.bus, opt); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if n := len(r.bus.Log()); n != 0 {
			t.Errorf("%s: %d transactions before the option was refused", name, n)
		}
	}
}

func TestAveragingIsEncodedInAVConf(t *testing.T) {
	for _, tc := range []struct {
		name string
		avg  hts221.Averaging
		want byte
	}{
		{"default", hts221.Averaging{}, 0x1B}, // 32 and 16: AVGH 3, AVGT 3
		{"the minimum", hts221.Averaging{Humidity: 4, Temperature: 2}, 0x00},
		{"the maximum", hts221.Averaging{Humidity: 512, Temperature: 256}, 0x3F},
		{"humidity only, temperature stays at 16", hts221.Averaging{Humidity: 4}, 0x18},
		{"temperature only, humidity stays at 32", hts221.Averaging{Temperature: 2}, 0x03},
		{"mixed", hts221.Averaging{Humidity: 128, Temperature: 8}, 0x15}, // AVGH 5, AVGT 2: 0b010_101
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			newSensor(t, r, hts221.WithAveraging(tc.avg))
			if got := r.model.Peek(0x10); got != tc.want {
				t.Errorf("AV_CONF = %#x, want %#x", got, tc.want)
			}
		})
	}
}

// Negative control, seen failing: a configure that leaves BDU out for one
// mode, or Close that writes 0 instead of the BDU bit alone. The model also
// reports any such write as a violation to the test.
func TestBDUIsOnInEveryWriteOfEveryPath(t *testing.T) {
	scenarios := map[string]func(t *testing.T, r *rig){
		"one-shot": func(t *testing.T, r *rig) {
			s := newSensor(t, r)
			mustRead(t, s)
			_ = s.Close(bg)
		},
		"continuous 1 Hz":    func(t *testing.T, r *rig) { newSensor(t, r, hts221.WithMode(hts221.Continuous(hts221.Rate1Hz))) },
		"continuous 7 Hz":    func(t *testing.T, r *rig) { newSensor(t, r, hts221.WithMode(hts221.Continuous(hts221.Rate7Hz))) },
		"continuous 12.5 Hz": func(t *testing.T, r *rig) { newSensor(t, r, hts221.WithMode(hts221.Continuous(hts221.Rate12Hz5))) },
		"with averaging": func(t *testing.T, r *rig) {
			newSensor(t, r, hts221.WithAveraging(hts221.Averaging{Humidity: 512, Temperature: 256}))
		},
		"after a reset": func(t *testing.T, r *rig) {
			s := newSensor(t, r, hts221.WithReadyTimeout(20*time.Millisecond))
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
			}
		})
	}
}

func mustRead(t *testing.T, s *hts221.Sensor) hts221.Sample {
	t.Helper()
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestContinuousModeReturnsTheLatestSampleAndFlagsStaleOnes(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, hts221.WithMode(hts221.Continuous(hts221.Rate7Hz)))
	before := len(r.bus.Log())

	first := mustRead(t, s) // nothing has been sampled yet
	if !first.Humidity.Flags.Has(vane.FlagStale) || !first.Temperature.Flags.Has(vane.FlagStale) {
		t.Errorf("a read before the first sample: flags %q, %q, want both stale", first.Humidity.Flags, first.Temperature.Flags)
	}

	r.model.Set(40, 25)
	r.model.NewSample()
	fresh := mustRead(t, s)
	if fresh.Humidity.Flags != 0 || fresh.Temperature.Flags != 0 {
		t.Errorf("a new sample is flagged %q, %q", fresh.Humidity.Flags, fresh.Temperature.Flags)
	}
	if fresh.Humidity.Raw != -500+2667 || fresh.Temperature.Raw != -200+200 { // 40 %RH: 20 above H0 = 2666.67 counts; 25 degC: 5 above T0 = 200
		t.Errorf("Raw = %d, %d", fresh.Humidity.Raw, fresh.Temperature.Raw)
	}

	again := mustRead(t, s) // the chip has produced nothing new
	if !again.Humidity.Flags.Has(vane.FlagStale) || !again.Temperature.Flags.Has(vane.FlagStale) {
		t.Errorf("a repeated read: flags %q, %q, want both stale", again.Humidity.Flags, again.Temperature.Flags)
	}
	if again.Humidity.Value != fresh.Humidity.Value {
		t.Errorf("a stale read changed the value: %v then %v", fresh.Humidity.Value, again.Humidity.Value)
	}
	for _, f := range []vane.Flag{first.Humidity.Flags, fresh.Humidity.Flags, again.Humidity.Flags} {
		if f.Has(vane.FlagOverrun) {
			t.Error("FlagOverrun set; the HTS221 has no overrun bit")
		}
	}
	for _, tx := range r.bus.Log()[before:] {
		if len(tx.R) == 0 {
			t.Errorf("continuous Read wrote to the chip: %x", tx.W)
		}
	}
}

// A reset in continuous mode raises no error and stops the samples, so the only
// symptom is that every read is stale. That limit is documented (ADR-0005); the
// test keeps it from being forgotten.
func TestAResetInContinuousModeShowsAsPermanentlyStaleReads(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, hts221.WithMode(hts221.Continuous(hts221.Rate1Hz)))
	r.model.Set(50, 50)
	r.model.NewSample()
	if mustRead(t, s).Humidity.Flags.Has(vane.FlagStale) {
		t.Fatal("the first sample is stale")
	}
	r.model.Reset()
	for range 5 {
		r.model.NewSample() // a powered-down chip ignores it
		if got := mustRead(t, s); !got.Humidity.Flags.Has(vane.FlagStale) {
			t.Fatalf("a read after a reset is not stale: %q", got.Humidity.Flags)
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
	if !errors.Is(err, vane.ErrClosed) || !errors.As(err, &se) || se.Op != "read" || se.Source.Address != 0x5F {
		t.Fatalf("error = %v, want a read SensorError wrapping ErrClosed", err)
	}
	if len(r.bus.Log()) != n {
		t.Error("a Read after Close used the bus")
	}
}

func TestACloseThatFailsStillClosesAndReportsOnce(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.bus.Inject(0x5F, vanetest.EIO().Times(1))
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
// Negative control, seen failing: not setting reinit on ErrNotReady leaves the
// chip unconfigured and every later Read times out.
func TestAResetIsRecoveredFromOnTheNextRead(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, hts221.WithReadyTimeout(20*time.Millisecond))
	r.model.Set(50, 50)
	mustRead(t, s)

	r.bus.Inject(0x5F, vanetest.Reset()) // fires inside the next transaction, before the trigger lands
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
	if got.Humidity.Raw != 3500 || got.Temperature.Raw != 1000 {
		t.Errorf("the recovered read = %d, %d, want 3500, 1000", got.Humidity.Raw, got.Temperature.Raw)
	}
	log := r.bus.Log()[before:]
	if len(log) < 3 || len(log[0].W) != 1 || log[0].W[0] != 0x0F {
		t.Errorf("recovery did not start by identifying the chip:\n%s", describe(log))
	}
	if r.model.Peek(0x20) != 0x84 {
		t.Errorf("CTRL_REG1 after recovery = %#x, want 0x84", r.model.Peek(0x20))
	}
	mustRead(t, s) // and it stays recovered
}

// A chip that stalled, as opposed to one that reset, still has its conversion
// pending. The driver finds that out from ONE_SHOT and waits for it instead of
// triggering another on top of it. Negative control, seen failing: clearing
// dirty on recovery without asking the chip.
func TestAChipThatStaysStalledKeepsReportingNotReady(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, hts221.WithReadyTimeout(15*time.Millisecond))
	r.model.StallReady(true)
	for i := range 3 {
		if _, err := s.Read(bg); !errors.Is(err, vane.ErrNotReady) {
			t.Fatalf("read %d: %v, want ErrNotReady", i, err)
		}
	}
	r.model.StallReady(false)
	got := mustRead(t, s)
	if got.Humidity.Raw == 0 {
		t.Errorf("the read after the stall ended returned an empty sample: %+v", got.Humidity)
	}
}

// Negative control, seen failing: without letting the pending conversion end
// before it triggers again, the second Read puts a trigger on top of a
// conversion still running, which the model reports as overlapping sequences.
func TestAReadCutShortDoesNotLeaveASecondTriggerOnTopOfTheFirst(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, hts221.WithReadyTimeout(time.Hour))
	r.model.Set(60, 60)
	r.model.StallReady(true)

	cctx, cancel := context.WithTimeout(bg, 15*time.Millisecond)
	defer cancel()
	if _, err := s.Read(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cut-short read = %v, want DeadlineExceeded", err)
	}
	r.model.StallReady(false) // the first conversion can finish now

	got := mustRead(t, s)
	// 60 %RH is 40 above H0: -500 + 5333; 60 degC is 40 above T0: -200 + 1600.
	if got.Humidity.Raw != 4833 || got.Temperature.Raw != 1400 {
		t.Errorf("Raw = %d, %d after recovering from a cut-short read, want 4833, 1400", got.Humidity.Raw, got.Temperature.Raw)
	}
}

// RNF-04: trigger, wait and read are one sequence per chip. Without the sensor's
// mutex, goroutines interleave their sequences and the model reports it.
// Negative control, seen failing: removing the lock in Read.
func TestConcurrentReadsDoNotInterleaveTheirSequences(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(2))
	s := newSensor(t, r)
	r.model.Set(50, 50)
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
				if got.Humidity.Raw != 3500 || got.Temperature.Raw != 1000 {
					t.Errorf("a concurrent read got %d, %d", got.Humidity.Raw, got.Temperature.Raw)
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
