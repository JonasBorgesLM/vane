package lps25h_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/vanetest"
)

var fixedTime = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return fixedTime }

func newSensor(t *testing.T, r *rig, opts ...lps25h.Option) *lps25h.Sensor {
	t.Helper()
	opts = append([]lps25h.Option{
		lps25h.WithBusName("/dev/i2c-1"),
		lps25h.WithClock(fixedClock),
		lps25h.WithPollInterval(time.Millisecond),
	}, opts...)
	s, err := lps25h.New(bg, r.bus, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadReturnsTheValuesTheChipMeasures(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.model.Set(1013.25, 22.5) // the model puts these at 4150272 and -9600 counts
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	src := vane.Source{Chip: vane.ChipLPS25H, Bus: "/dev/i2c-1", Address: 0x5C}
	wantP := vane.Measurement[vane.Hectopascal]{Value: 1013.25, Raw: 4150272, Source: src, Time: fixedTime}
	wantT := vane.Measurement[vane.Celsius]{Value: 22.5, Raw: -9600, Source: src, Time: fixedTime}
	if got.Pressure != wantP {
		t.Errorf("Pressure = %+v, want %+v", got.Pressure, wantP)
	}
	if got.Temperature != wantT {
		t.Errorf("Temperature = %+v, want %+v", got.Temperature, wantT)
	}
}

// The model rounds what it is given to a whole count: half a count is
// 1/8192 hPa and 1/960 degC.
func TestReadTracksWhatIsSet(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	for _, tc := range []struct{ p, t float64 }{{260, -20}, {1260, 85}, {800.37, 5.5}, {1013.25, 42.5}, {977.123, 31.4}, {1200, 100}} {
		r.model.Set(vanetest.LPS25HPressure(tc.p), vanetest.LPS25HTemperature(tc.t))
		got, err := s.Read(bg)
		if err != nil {
			t.Fatal(err)
		}
		if d := math.Abs(float64(got.Pressure.Value) - tc.p); d > 1.0/8192+1e-9 {
			t.Errorf("pressure: set %v, read %v", tc.p, got.Pressure.Value)
		}
		if d := math.Abs(float64(got.Temperature.Value) - tc.t); d > 1.0/960+1e-9 {
			t.Errorf("temperature: set %v, read %v", tc.t, got.Temperature.Value)
		}
		if got.Pressure.Flags != 0 || got.Temperature.Flags != 0 {
			t.Errorf("flags on an ordinary reading: %q %q", got.Pressure.Flags, got.Temperature.Flags)
		}
	}
}

// Negative control, seen failing: reading the 24-bit pressure as unsigned.
func TestANegativePressureKeepsItsSign(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.model.Set(-10, 42.5)
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pressure.Raw != -40960 || got.Pressure.Value != -10 {
		t.Errorf("pressure = %v (raw %d), want -10 (raw -40960)", got.Pressure.Value, got.Pressure.Raw)
	}
	if got.Pressure.Flags != vane.FlagOutOfRange {
		t.Errorf("a negative absolute pressure is outside the operating range; flags %q", got.Pressure.Flags)
	}
}

func TestEveryReadIsAFreshSample(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.model.Set(900, 30)
	first, _ := s.Read(bg)
	r.model.Set(1100, 30)
	second, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	if first.Pressure.Raw == second.Pressure.Raw || math.Abs(float64(second.Pressure.Value)-1100) > 0.001 {
		t.Fatalf("second read = %+v, want the new value, not the previous sample", second.Pressure)
	}
}

func TestFlagsDescribeReadingsOutsideTheRange(t *testing.T) {
	tests := []struct {
		name         string
		p, t         float64
		wantP, wantT vane.Flag
	}{
		{"pressure above the full scale", 1300, 20, vane.FlagOutOfRange, 0},
		{"pressure below the lower end", 200, 20, vane.FlagOutOfRange, 0},
		{"the ends of the pressure range are inside it", 1260, 20, 0, 0},
		{"the lower end is inside", 260, 20, 0, 0},
		{"temperature above the range", 1000, 106, 0, vane.FlagOutOfRange},
		{"the top of the temperature range is inside it", 1000, 105, 0, 0},
		{"both at once", 1300, 110, vane.FlagOutOfRange, vane.FlagOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			s := newSensor(t, r)
			r.model.Set(vanetest.LPS25HPressure(tc.p), vanetest.LPS25HTemperature(tc.t))
			got, err := s.Read(bg)
			if err != nil {
				t.Fatalf("a flagged reading must not be an error: %v", err)
			}
			if got.Pressure.Flags != tc.wantP || got.Temperature.Flags != tc.wantT {
				t.Errorf("flags = %q, %q; want %q, %q", got.Pressure.Flags, got.Temperature.Flags, tc.wantP, tc.wantT)
			}
			if math.Abs(float64(got.Pressure.Value)-tc.p) > 0.001 {
				t.Errorf("an out-of-range pressure was altered: set %v, read %v", tc.p, got.Pressure.Value)
			}
		})
	}
}

func TestReadWaitsForTheConversion(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(3))
	s := newSensor(t, r)
	before := len(r.bus.Log())
	if _, err := s.Read(bg); err != nil {
		t.Fatal(err)
	}
	var statusReads int
	for _, tx := range r.bus.Log()[before:] {
		if len(tx.W) == 1 && tx.W[0] == 0x27 {
			statusReads++
		}
	}
	if statusReads != 4 {
		t.Errorf("status read %d times, want 4: three not-ready answers and the ready one", statusReads)
	}
}

// Negative control, seen failing: treating either data-ready bit as enough reads
// a pressure the chip has not produced yet.
func TestReadWaitsForBothDataReadyBits(t *testing.T) {
	r := newRig(t, vanetest.WithStaggeredReady())
	s := newSensor(t, r)
	r.model.Set(1013.25, 22.5)
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pressure.Raw != 4150272 || got.Temperature.Raw != -9600 {
		t.Fatalf("Raw = %d, %d; want 4150272, -9600: the pressure was read before it was ready", got.Pressure.Raw, got.Temperature.Raw)
	}
}

func TestReadGivesUpWhenTheChipNeverFinishes(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, lps25h.WithReadyTimeout(30*time.Millisecond))
	r.model.StallReady(true)
	start := time.Now()
	_, err := s.Read(bg)
	var se *vane.SensorError
	if !errors.Is(err, vane.ErrNotReady) || !errors.As(err, &se) || se.Op != "wait" || se.Reg != 0x27 {
		t.Fatalf("error = %v, want a wait SensorError at register 0x27 wrapping ErrNotReady", err)
	}
	if d := time.Since(start); d < 30*time.Millisecond || d > 5*time.Second {
		t.Errorf("gave up after %v, want about the 30ms timeout", d)
	}
}

func TestReadStopsWhenTheContextEnds(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, lps25h.WithReadyTimeout(time.Hour))
	r.model.StallReady(true)
	cctx, cancel := context.WithTimeout(bg, 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Read(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to notice the deadline", d)
	}
}

func TestEachStepReportsItsOwnFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		after   int
		wantOp  string
		wantReg uint8
	}{{"trigger", 0, "trigger", 0x21}, {"wait", 1, "wait", 0x27}, {"read", 2, "read", 0x28}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			s := newSensor(t, r)
			r.bus.Inject(0x5C, vanetest.EIO().After(tc.after).Times(1))
			_, err := s.Read(bg)
			var se *vane.SensorError
			if !errors.As(err, &se) || se.Op != tc.wantOp || se.Reg != tc.wantReg || !errors.Is(err, vanetest.ErrIO) {
				t.Fatalf("error = %v, want a %s SensorError at 0x%02x wrapping ErrIO", err, tc.wantOp, tc.wantReg)
			}
		})
	}
}

// directBus hands transactions straight to a model, with none of FakeBus's
// logging, so what is measured is the driver and not the test double.
type directBus struct{ m *vanetest.LPS25HModel }

func (b directBus) Tx(_ context.Context, _ uint16, w, r []byte) error { return b.m.Tx(w, r) }

func newDirect(tb testing.TB, polls int) *lps25h.Sensor {
	tb.Helper()
	m := vanetest.NewLPS25H(vanetest.WithConversionPolls(polls))
	m.Set(1013.25, 22.5)
	s, err := lps25h.New(bg, directBus{m}, lps25h.WithClock(fixedClock), lps25h.WithPollInterval(time.Microsecond))
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

// RNF-08: no allocation per read at the driver level, whether the data is ready
// on the first poll or the read has to sleep first. Waiting is the usual case on
// the Pi, so both are asserted.
func TestReadDoesNotAllocate(t *testing.T) {
	for name, polls := range map[string]int{"data ready at once": 0, "after one wait": 1} {
		s := newDirect(t, polls)
		if n := testing.AllocsPerRun(200, func() { _, _ = s.Read(bg) }); n != 0 {
			t.Errorf("%s: Read allocates %v times per call, want 0", name, n)
		}
	}
}

func BenchmarkRead(b *testing.B) {
	s := newDirect(b, 0)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Read(bg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadThatWaits(b *testing.B) {
	s := newDirect(b, 1)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Read(bg); err != nil {
			b.Fatal(err)
		}
	}
}
