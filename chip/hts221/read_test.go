package hts221_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

var fixedTime = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return fixedTime }

func newSensor(t *testing.T, r *rig, opts ...hts221.Option) *hts221.Sensor {
	t.Helper()
	opts = append([]hts221.Option{
		hts221.WithBusName("/dev/i2c-1"),
		hts221.WithClock(fixedClock),
		hts221.WithPollInterval(time.Millisecond),
	}, opts...)
	s, err := hts221.New(ctx, r.bus, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadReturnsTheValuesTheChipMeasures(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.model.Set(50, 50) // the model puts these at 3500 and 1000 counts
	got, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src := vane.Source{Chip: vane.ChipHTS221, Bus: "/dev/i2c-1", Address: 0x5F}
	wantH := vane.Measurement[vane.RelativeHumidity]{Value: 50, Raw: 3500, Source: src, Time: fixedTime}
	wantT := vane.Measurement[vane.Celsius]{Value: 50, Raw: 1000, Source: src, Time: fixedTime}
	if got.Humidity != wantH {
		t.Errorf("Humidity = %+v, want %+v", got.Humidity, wantH)
	}
	if got.Temperature != wantT {
		t.Errorf("Temperature = %+v, want %+v", got.Temperature, wantT)
	}
}

// The model rounds the value it is given to a whole count, so a reading is
// within half a count of what was set: 0.00375 %RH and 0.0125 degC here.
func TestReadTracksWhatIsSet(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	for _, tc := range []struct{ rh, t float64 }{{20, 20}, {80, 80}, {33.3, -12.5}, {0.5, 99.9}, {99.5, 0}, {61.234, 37.8}} {
		r.model.Set(vanetest.HTS221Humidity(tc.rh), vanetest.HTS221Temperature(tc.t))
		got, err := s.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if d := math.Abs(float64(got.Humidity.Value) - tc.rh); d > 0.00376 {
			t.Errorf("humidity: set %v, read %v", tc.rh, got.Humidity.Value)
		}
		if d := math.Abs(float64(got.Temperature.Value) - tc.t); d > 0.01251 {
			t.Errorf("temperature: set %v, read %v", tc.t, got.Temperature.Value)
		}
		if got.Humidity.Flags != 0 || got.Temperature.Flags != 0 {
			t.Errorf("flags on an ordinary reading: %q %q", got.Humidity.Flags, got.Temperature.Flags)
		}
	}
}

func TestEveryReadIsAFreshSample(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.model.Set(30, 30)
	first, _ := s.Read(ctx)
	r.model.Set(70, 70)
	second, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Humidity.Raw == second.Humidity.Raw || math.Abs(float64(second.Humidity.Value)-70) > 0.01 {
		t.Fatalf("second read = %+v, want the new value, not the previous sample", second.Humidity)
	}
}

func TestFlagsDescribeReadingsOutsideTheRange(t *testing.T) {
	tests := []struct {
		name         string
		rh, t        float64
		wantH, wantT vane.Flag
		wantHValue   float64
	}{
		{"humidity above 100 is clamped", 130, 20, vane.FlagClamped, 0, 100},
		{"humidity below 0 is clamped", -10, 20, vane.FlagClamped, 0, 0},
		{"temperature above the range is flagged and kept", 50, 130, 0, vane.FlagOutOfRange, 50},
		{"temperature below the range is flagged and kept", 50, -45, 0, vane.FlagOutOfRange, 50},
		{"the ends of the temperature range are inside it", 50, 120, 0, 0, 50},
		{"both at once", 150, 150, vane.FlagClamped, vane.FlagOutOfRange, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			s := newSensor(t, r)
			r.model.Set(vanetest.HTS221Humidity(tc.rh), vanetest.HTS221Temperature(tc.t))
			got, err := s.Read(ctx)
			if err != nil {
				t.Fatalf("a flagged reading must not be an error: %v", err)
			}
			if got.Humidity.Flags != tc.wantH || got.Temperature.Flags != tc.wantT {
				t.Errorf("flags = %q, %q; want %q, %q", got.Humidity.Flags, got.Temperature.Flags, tc.wantH, tc.wantT)
			}
			if got.Humidity.Value != vane.RelativeHumidity(tc.wantHValue) && tc.wantH != 0 {
				t.Errorf("humidity = %v, want %v", got.Humidity.Value, tc.wantHValue)
			}
			if tc.wantT != 0 && math.Abs(float64(got.Temperature.Value)-tc.t) > 0.02 {
				t.Errorf("an out-of-range temperature was altered: set %v, read %v", tc.t, got.Temperature.Value)
			}
		})
	}
}

func TestReadWaitsForTheConversion(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(3))
	s := newSensor(t, r)
	r.model.Set(50, 50)
	before := len(r.bus.Log())
	if _, err := s.Read(ctx); err != nil {
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

func TestReadGivesUpWhenTheChipNeverFinishes(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, hts221.WithReadyTimeout(30*time.Millisecond))
	r.bus.Inject(0x5F, vanetest.StallReady())
	start := time.Now()
	_, err := s.Read(ctx)
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
	s := newSensor(t, r, hts221.WithReadyTimeout(time.Hour))
	r.bus.Inject(0x5F, vanetest.StallReady())
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Read(cctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to notice the deadline", d)
	}
}

func TestEachStepReportsItsOwnFailure(t *testing.T) {
	tests := []struct {
		name    string
		after   int // transactions of New and of the earlier steps to let through
		wantOp  string
		wantReg uint8
	}{
		{"trigger", 0, "trigger", 0x21},
		{"wait", 1, "wait", 0x27},
		{"read", 2, "read", 0x28},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			s := newSensor(t, r)
			r.bus.Inject(0x5F, vanetest.EIO().After(tc.after).Times(1))
			_, err := s.Read(ctx)
			var se *vane.SensorError
			if !errors.As(err, &se) || se.Op != tc.wantOp || se.Reg != tc.wantReg || !errors.Is(err, vanetest.ErrIO) {
				t.Fatalf("error = %v, want a %s SensorError at 0x%02x wrapping ErrIO", err, tc.wantOp, tc.wantReg)
			}
		})
	}
}

// directBus hands transactions straight to a model, with none of FakeBus's
// logging, so what is measured is the driver and not the test double.
type directBus struct{ m *vanetest.HTS221Model }

func (b directBus) Tx(_ context.Context, _ uint16, w, r []byte) error { return b.m.Tx(w, r) }

func newDirect(tb testing.TB, polls int) *hts221.Sensor {
	tb.Helper()
	m := vanetest.NewHTS221(vanetest.WithConversionPolls(polls))
	m.Set(50, 50)
	s, err := hts221.New(ctx, directBus{m}, hts221.WithClock(fixedClock), hts221.WithPollInterval(time.Microsecond))
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

// RNF-08: no allocation per read at the driver level, whether the data is
// ready on the first poll or the read has to sleep first. Waiting is the usual
// case on the Pi, so both are asserted.
func TestReadDoesNotAllocate(t *testing.T) {
	for name, polls := range map[string]int{"data ready at once": 0, "after one wait": 1} {
		s := newDirect(t, polls)
		if n := testing.AllocsPerRun(200, func() { _, _ = s.Read(ctx) }); n != 0 {
			t.Errorf("%s: Read allocates %v times per call, want 0", name, n)
		}
	}
}

func BenchmarkRead(b *testing.B) {
	s := newDirect(b, 0)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Read(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadThatWaits(b *testing.B) {
	s := newDirect(b, 1)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Read(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// Negative control, seen failing: reading Raw as unsigned.
func TestRawIsSignExtended(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.model.Set(0, 0) // 20 %RH below H0 (-500 - 2667) and 20 degC below T0 (-200 - 800)
	got, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Humidity.Raw != -3167 || got.Temperature.Raw != -1000 {
		t.Errorf("Raw = %d, %d; want -3167, -1000", got.Humidity.Raw, got.Temperature.Raw)
	}
	if math.Abs(float64(got.Humidity.Value)) > 0.004 || math.Abs(float64(got.Temperature.Value)) > 0.013 {
		t.Errorf("values = %v, %v, want about 0", got.Humidity.Value, got.Temperature.Value)
	}
}

// Negative control, seen failing: treating either data-ready bit as enough
// reads a humidity the chip has not produced yet.
func TestReadWaitsForBothDataReadyBits(t *testing.T) {
	r := newRig(t, vanetest.WithStaggeredReady())
	s := newSensor(t, r)
	r.model.Set(50, 50)
	got, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Humidity.Raw != 3500 || got.Temperature.Raw != 1000 {
		t.Fatalf("Raw = %d, %d; want 3500, 1000: the humidity was read before it was ready", got.Humidity.Raw, got.Temperature.Raw)
	}
}
