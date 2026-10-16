package vanetest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/sensehat"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// board is the method set a consumer such as beacon depends on. Both the real
// Device and the FakeBoard must satisfy it, which is what lets a test use the
// second where production uses the first.
//
// Negative control, seen failing: removing or renaming any method of FakeBoard
// stops this file compiling.
type board interface {
	Environment(ctx context.Context) (sensehat.Environment, error)
	Sensors() []sensehat.SensorInfo
	Board() sensehat.BoardInfo
	Close() error
}

var (
	_ board = (*sensehat.Device)(nil)
	_ board = (*vanetest.FakeBoard)(nil)
)

var (
	t0   = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	ctx0 = context.Background()
)

func healthy() vanetest.Step { return vanetest.Healthy(t0, 50, 1013.25, 25, 22.5) }

func TestHealthyHasEveryFieldTheDeviceFills(t *testing.T) {
	env, err := vanetest.NewFakeBoard(healthy()).Environment(ctx0)
	if err != nil {
		t.Fatal(err)
	}
	if env.Humidity.Err != nil || env.Humidity.Value != 50 || env.Humidity.Time != t0 ||
		env.Humidity.Source != (vane.Source{Chip: vane.ChipHTS221, Bus: "/dev/i2c-1", Address: 0x5F}) {
		t.Errorf("Humidity = %+v", env.Humidity)
	}
	if env.Pressure.Err != nil || env.Pressure.Value != 1013.25 ||
		env.Pressure.Source != (vane.Source{Chip: vane.ChipLPS25H, Bus: "/dev/i2c-1", Address: 0x5C}) {
		t.Errorf("Pressure = %+v", env.Pressure)
	}
	if len(env.Temperatures) != 2 || env.Temperatures[0].Source.Chip != vane.ChipHTS221 || env.Temperatures[0].Value != 25 ||
		env.Temperatures[1].Source.Chip != vane.ChipLPS25H || env.Temperatures[1].Value != 22.5 {
		t.Errorf("Temperatures = %+v, want the HTS221's then the LPS25H's", env.Temperatures)
	}
}

func TestTheScriptIsPlayedInOrderAndEndsWithAnError(t *testing.T) {
	f := vanetest.NewFakeBoard(vanetest.Healthy(t0, 40, 1000, 20, 20), vanetest.Healthy(t0, 60, 1010, 30, 30))
	for i, want := range []vane.RelativeHumidity{40, 60} {
		env, err := f.Environment(ctx0)
		if err != nil || env.Humidity.Value != want {
			t.Fatalf("call %d: %v, %v; want humidity %v", i, env.Humidity.Value, err, want)
		}
	}
	env, err := f.Environment(ctx0)
	if !errors.Is(err, vanetest.ErrScriptEnded) || env.Humidity.Value != 0 || len(env.Temperatures) != 0 {
		t.Errorf("past the end: %+v, %v; want ErrScriptEnded and nothing else", env, err)
	}
	if f.Calls() != 3 {
		t.Errorf("Calls() = %d, want 3 (the call that found the script ended counts)", f.Calls())
	}
}

func TestTimesAndForeverRepeatAStep(t *testing.T) {
	f := vanetest.NewFakeBoard(
		vanetest.Healthy(t0, 40, 1000, 20, 20).Times(3),
		vanetest.Healthy(t0, 60, 1010, 30, 30).Forever(),
	)
	got := make([]vane.RelativeHumidity, 0, 8)
	for range 8 {
		env, err := f.Environment(ctx0)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, env.Humidity.Value)
	}
	want := []vane.RelativeHumidity{40, 40, 40, 60, 60, 60, 60, 60}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("humidity over 8 calls = %v, want %v", got, want)
		}
	}
}

// Negative control, seen failing: Failing that does not also fail the matching
// temperature, or that leaves the failed reading without its Source.
func TestFailingOneChipLeavesTheOtherAndSetsNoTopLevelError(t *testing.T) {
	cause := errors.New("bus stuck")
	env, err := vanetest.NewFakeBoard(healthy().Failing(vane.ChipLPS25H, cause)).Environment(ctx0)
	if err != nil {
		t.Fatalf("one chip failing is not a top-level error on a Device, so not here: %v", err)
	}
	var se *vane.SensorError
	if !errors.Is(env.Pressure.Err, cause) || !errors.As(env.Pressure.Err, &se) || se.Source.Chip != vane.ChipLPS25H || se.Op != "read" {
		t.Errorf("Pressure.Err = %v, want a read SensorError for the LPS25H wrapping the cause", env.Pressure.Err)
	}
	if env.Pressure.Source.Chip != vane.ChipLPS25H || env.Pressure.Value != 0 {
		t.Errorf("a failed reading should keep its Source and carry no value: %+v", env.Pressure)
	}
	if env.Humidity.Err != nil || env.Humidity.Value != 50 {
		t.Errorf("the other chip was affected: %+v", env.Humidity)
	}
	if env.Temperatures[0].Err != nil || env.Temperatures[1].Err == nil || env.Temperatures[1].Source.Chip != vane.ChipLPS25H {
		t.Errorf("Temperatures = %+v, want only the LPS25H's failed", env.Temperatures)
	}
}

func TestFailingEveryChipSetsTheTopLevelErrorAsADeviceDoes(t *testing.T) {
	a, b := errors.New("humidity side"), errors.New("pressure side")
	_, err := vanetest.NewFakeBoard(healthy().Failing(vane.ChipHTS221, a).Failing(vane.ChipLPS25H, b)).Environment(ctx0)
	if err == nil || !errors.Is(err, a) || !errors.Is(err, b) {
		t.Fatalf("error = %v, want both reasons joined", err)
	}
}

func TestFailAndResultAndDelayed(t *testing.T) {
	boom := errors.New("boom")
	if env, err := vanetest.NewFakeBoard(vanetest.Fail(boom)).Environment(ctx0); !errors.Is(err, boom) || len(env.Temperatures) != 0 {
		t.Errorf("Fail: %+v, %v", env, err)
	}
	custom := sensehat.Environment{Humidity: sensehat.Reading[vane.RelativeHumidity]{Measurement: vane.Measurement[vane.RelativeHumidity]{Value: 12, Flags: vane.FlagStale}}}
	got, err := vanetest.NewFakeBoard(vanetest.Result(custom, nil)).Environment(ctx0)
	if err != nil || got.Humidity.Value != 12 || !got.Humidity.Flags.Has(vane.FlagStale) {
		t.Errorf("Result: %+v, %v", got, err)
	}
	start := time.Now()
	if _, err := vanetest.NewFakeBoard(healthy().Delayed(30 * time.Millisecond)).Environment(ctx0); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Errorf("a delayed step took %v, want at least 30ms", d)
	}
}

func TestADelayIsCutShortByTheContext(t *testing.T) {
	f := vanetest.NewFakeBoard(healthy().Delayed(time.Hour))
	cctx, cancel := context.WithTimeout(ctx0, 10*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.Environment(cctx); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hour-long delay was not cut short by the context's deadline")
	}
}

func TestAContextThatHasEndedDoesNotUseUpAStep(t *testing.T) {
	f := vanetest.NewFakeBoard(healthy())
	cctx, cancel := context.WithCancel(ctx0)
	cancel()
	if _, err := f.Environment(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if env, err := f.Environment(ctx0); err != nil || env.Humidity.Value != 50 {
		t.Errorf("the step was used up by a call that never started: %+v, %v", env, err)
	}
}

func TestCloseEndsTheBoardAndIsIdempotent(t *testing.T) {
	f := vanetest.NewFakeBoard(healthy().Forever())
	if f.Closed() {
		t.Fatal("a new board is closed")
	}
	for range 3 {
		if err := f.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	}
	if !f.Closed() {
		t.Error("Closed() = false after Close")
	}
	before := f.Calls()
	if env, err := f.Environment(ctx0); !errors.Is(err, vane.ErrClosed) || env.Humidity.Value != 0 {
		t.Errorf("Environment after Close = %+v, %v; want ErrClosed", env, err)
	}
	if f.Calls() != before {
		t.Error("a call on a closed board was counted")
	}
}

func TestSensorsAndBoardAreSettableAndCopied(t *testing.T) {
	f := vanetest.NewFakeBoard()
	got := f.Sensors()
	if len(got) != 2 || got[0].Source.Chip != vane.ChipHTS221 || got[1].Source.Chip != vane.ChipLPS25H ||
		got[0].State != sensehat.StateReady || got[1].State != sensehat.StateReady {
		t.Fatalf("default Sensors = %+v", got)
	}
	got[0].State = sensehat.StateFailed
	if f.Sensors()[0].State != sensehat.StateReady {
		t.Error("modifying the result of Sensors changed the board")
	}
	owned := &vane.KernelOwnedError{Source: got[1].Source, Driver: "st-press-i2c"}
	infos := []sensehat.SensorInfo{
		{Source: got[0].Source, State: sensehat.StateReady},
		{Source: got[1].Source, State: sensehat.StateKernelOwned, Err: owned},
	}
	f.SetSensors(infos...)
	if s := f.Sensors(); s[1].State != sensehat.StateKernelOwned || !errors.Is(s[1].Err, vane.ErrKernelOwned) {
		t.Errorf("Sensors after SetSensors = %+v", s)
	}
	infos[1].State = sensehat.StateReady // the caller reuses its slice afterwards
	if f.Sensors()[1].State != sensehat.StateKernelOwned {
		t.Error("SetSensors kept the caller's slice, so changing it changed the board")
	}
	if f.Board().Present {
		t.Error("a new board reports a HAT EEPROM")
	}
	f.SetBoard(sensehat.BoardInfo{Present: true, Product: "Sense HAT"})
	if b := f.Board(); !b.Present || b.Product != "Sense HAT" {
		t.Errorf("Board = %+v", b)
	}
}

// The script must not be changed by whoever reads an answer, or a step that
// repeats would drift.
func TestAnAnswerIsACopy(t *testing.T) {
	f := vanetest.NewFakeBoard(healthy().Forever())
	first, _ := f.Environment(ctx0)
	first.Temperatures[0].Value = 999
	first.Temperatures = append(first.Temperatures, sensehat.Reading[vane.Celsius]{})
	second, _ := f.Environment(ctx0)
	if len(second.Temperatures) != 2 || second.Temperatures[0].Value != 25 {
		t.Errorf("the second answer shows the first caller's changes: %+v", second.Temperatures)
	}
}

func TestConcurrentCallsEachGetOneStep(t *testing.T) {
	f := vanetest.NewFakeBoard(healthy().Times(40))
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, ended := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				_, err := f.Environment(ctx0)
				mu.Lock()
				if err == nil {
					ok++
				} else if errors.Is(err, vanetest.ErrScriptEnded) {
					ended++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 40 || ended != 40 {
		t.Errorf("%d answered and %d past the end; want 40 and 40: every step used exactly once", ok, ended)
	}
}

// The two implementations must not differ in behaviour, only in where the
// answers come from. Each scenario is run on a real Device over the chip models
// and on a FakeBoard scripted to match, and the shape of the answer compared:
// which readings carry an error, which chips they name, and whether the call
// itself failed. Negative control, seen failing: a Failing that never sets the
// top-level error, or a FakeBoard that does not return ErrClosed.
func TestAFakeBoardBehavesLikeADeviceForTheSameScenario(t *testing.T) {
	type shape struct {
		humErr, presErr bool
		temps           []bool // Err != nil, per temperature, in order
		tempChips       []vane.Chip
		topLevel        bool
	}
	shapeOf := func(e sensehat.Environment, err error) shape {
		s := shape{humErr: e.Humidity.Err != nil, presErr: e.Pressure.Err != nil, topLevel: err != nil}
		for _, r := range e.Temperatures {
			s.temps = append(s.temps, r.Err != nil)
			s.tempChips = append(s.tempChips, r.Source.Chip)
		}
		return s
	}
	same := func(a, b shape) bool {
		if a.humErr != b.humErr || a.presErr != b.presErr || a.topLevel != b.topLevel || len(a.temps) != len(b.temps) {
			return false
		}
		for i := range a.temps {
			if a.temps[i] != b.temps[i] || a.tempChips[i] != b.tempChips[i] {
				return false
			}
		}
		return true
	}
	newDevice := func(t *testing.T) (*sensehat.Device, *vanetest.FakeBus) {
		t.Helper()
		bus := vanetest.NewFakeBus(vanetest.NewHTS221(), vanetest.NewLPS25H())
		d, err := sensehat.Open(ctx0, sensehat.WithBus(bus), sensehat.WithKernelCheck(false))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		return d, bus
	}
	cases := []struct {
		name   string
		device func(d *sensehat.Device, bus *vanetest.FakeBus)
		fake   func() *vanetest.FakeBoard
	}{
		{"both chips deliver", func(*sensehat.Device, *vanetest.FakeBus) {},
			func() *vanetest.FakeBoard { return vanetest.NewFakeBoard(healthy()) }},
		{"the HTS221 fails", func(_ *sensehat.Device, bus *vanetest.FakeBus) { bus.Inject(0x5F, vanetest.EIO()) },
			func() *vanetest.FakeBoard {
				return vanetest.NewFakeBoard(healthy().Failing(vane.ChipHTS221, vanetest.ErrIO))
			}},
		{"the LPS25H fails", func(_ *sensehat.Device, bus *vanetest.FakeBus) { bus.Inject(0x5C, vanetest.NACK()) },
			func() *vanetest.FakeBoard {
				return vanetest.NewFakeBoard(healthy().Failing(vane.ChipLPS25H, vane.ErrNotPresent))
			}},
		{"both chips fail", func(_ *sensehat.Device, bus *vanetest.FakeBus) {
			bus.Inject(0x5F, vanetest.EIO())
			bus.Inject(0x5C, vanetest.EIO())
		}, func() *vanetest.FakeBoard {
			return vanetest.NewFakeBoard(healthy().Failing(vane.ChipHTS221, vanetest.ErrIO).Failing(vane.ChipLPS25H, vanetest.ErrIO))
		}},
		{"the board is closed", func(d *sensehat.Device, _ *vanetest.FakeBus) { _ = d.Close() },
			func() *vanetest.FakeBoard { f := vanetest.NewFakeBoard(healthy()); _ = f.Close(); return f }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, bus := newDevice(t)
			tc.device(d, bus)
			realEnv, realErr := d.Environment(ctx0)
			fakeEnv, fakeErr := tc.fake().Environment(ctx0)
			if rs, fs := shapeOf(realEnv, realErr), shapeOf(fakeEnv, fakeErr); !same(rs, fs) {
				t.Errorf("a FakeBoard answers differently from a Device:\n  device: %+v\n  fake:   %+v", rs, fs)
			}
			if errors.Is(realErr, vane.ErrClosed) != errors.Is(fakeErr, vane.ErrClosed) {
				t.Errorf("ErrClosed: device %v, fake %v", realErr, fakeErr)
			}
		})
	}
}

// How a consumer such as beacon tests its adapter: it asks for the method set
// it needs, and the test hands it a FakeBoard.
func ExampleFakeBoard() {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	var b board = vanetest.NewFakeBoard(
		vanetest.Healthy(at, 50, 1013.25, 25, 22.5),
		vanetest.Healthy(at, 50, 1013.25, 25, 22.5).Failing(vane.ChipLPS25H, vane.ErrNotPresent),
	)
	defer b.Close()

	for i := 1; i <= 2; i++ {
		env, err := b.Environment(context.Background())
		fmt.Printf("read %d: error=%v humidity=%.0f pressure ok=%v\n", i, err, float64(env.Humidity.Value), env.Pressure.Err == nil)
	}
	// Output:
	// read 1: error=<nil> humidity=50 pressure ok=true
	// read 2: error=<nil> humidity=50 pressure ok=false
}

// The Environment has no IMU reading (ADR-0008), so failing the LSM9DS1 leaves
// a step as it was.
func TestFailingTheIMULeavesTheEnvironmentAlone(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	b := vanetest.NewFakeBoard(vanetest.Healthy(at, 50, 1013.25, 25, 22.5).Failing(vane.ChipLSM9DS1, vane.ErrNotPresent))
	env, err := b.Environment(t.Context())
	if err != nil || env.Humidity.Err != nil || env.Pressure.Err != nil {
		t.Fatalf("err=%v humidity=%v pressure=%v, want everything valid", err, env.Humidity.Err, env.Pressure.Err)
	}
	for _, r := range env.Temperatures {
		if r.Err != nil {
			t.Fatalf("temperature from %s failed: %v", r.Source.Chip, r.Err)
		}
	}
}
