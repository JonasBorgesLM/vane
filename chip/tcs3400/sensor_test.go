package tcs3400_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/tcs3400"
	"github.com/JonasBorgesLM/vane/vanetest"
)

var (
	bg        = context.Background()
	fixedTime = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
)

type reporter struct{ t *testing.T }

func (r *reporter) Errorf(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf("protocol violation: "+format, args...)
}

type rig struct {
	m   *vanetest.TCS3400Model
	bus *vanetest.FakeBus
}

func newRig(t *testing.T, opts ...vanetest.ModelOption) *rig {
	t.Helper()
	m := vanetest.NewTCS3400(append([]vanetest.ModelOption{vanetest.WithReporter(&reporter{t: t}), vanetest.WithConversionPolls(2)}, opts...)...)
	return &rig{m: m, bus: vanetest.NewFakeBus(m)}
}

func newSensor(t *testing.T, r *rig, opts ...tcs3400.Option) *tcs3400.Sensor {
	t.Helper()
	s, err := tcs3400.New(bg, r.bus, append([]tcs3400.Option{
		tcs3400.WithBusName("/dev/i2c-1"),
		tcs3400.WithClock(func() time.Time { return fixedTime }),
		tcs3400.WithPollInterval(time.Microsecond),
	}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writes(bus *vanetest.FakeBus) []vanetest.Tx {
	var out []vanetest.Tx
	for _, tx := range bus.Log() {
		if len(tx.W) > 1 && len(tx.R) == 0 && tx.Err == nil {
			out = append(out, tx)
		}
	}
	return out
}

func describe(log []vanetest.Tx) string {
	var s string
	for i, tx := range log {
		s += fmt.Sprintf("  %d: addr=%#x w=%x r=%x err=%v\n", i, tx.Addr, tx.W, tx.R, tx.Err)
	}
	return s
}

// RF-01. Negative control: configuring before identify makes the first
// transaction a write.
func TestTheIDIsReadBeforeAnythingIsWritten(t *testing.T) {
	r := newRig(t)
	newSensor(t, r)
	log := r.bus.Log()
	if len(log) == 0 || len(log[0].W) != 1 || log[0].W[0] != 0x92 || len(log[0].R) != 1 {
		t.Fatalf("the first transaction is not the ID read:\n%s", describe(log))
	}
}

func TestAWrongChipIsRefusedAndNothingIsWritten(t *testing.T) {
	for _, id := range []uint8{0x00, 0x91, 0x92, 0xA0, 0x68} {
		r := newRig(t, vanetest.WithWhoAmI(id))
		_, err := tcs3400.New(bg, r.bus)
		var ie *vane.IdentityError
		if !errors.As(err, &ie) || !errors.Is(err, vane.ErrUnexpectedChip) {
			t.Errorf("ID %#x: %v, want an IdentityError", id, err)
		}
		if w := writes(r.bus); len(w) != 0 {
			t.Errorf("ID %#x: %d writes after a failed identification", id, len(w))
		}
	}
}

// The ordering table and the ID register (datasheet pp. 20, 29), repeated here.
func TestVariantAndPart(t *testing.T) {
	for _, tc := range []struct {
		addr uint16
		id   uint8
		v    tcs3400.Variant
		part string
	}{
		{0x39, 0x90, tcs3400.VariantVDDBus, "TCS34001"},
		{0x39, 0x93, tcs3400.Variant1V8Bus, "TCS34003"},
		{0x29, 0x90, tcs3400.VariantVDDBus, "TCS34005"},
		{0x29, 0x93, tcs3400.Variant1V8Bus, "TCS34007"},
	} {
		r := newRig(t, vanetest.WithModelAddress(tc.addr), vanetest.WithWhoAmI(tc.id))
		s := newSensor(t, r, tcs3400.WithAddress(tc.addr))
		if s.Variant() != tc.v || s.Part() != tc.part || s.Source().Address != tc.addr {
			t.Errorf("0x%02x/%#x: variant %v part %q source %+v, want %v %q", tc.addr, tc.id, s.Variant(), s.Part(), s.Source(), tc.v, tc.part)
		}
	}
}

func TestIdentifyWritesNothing(t *testing.T) {
	r := newRig(t)
	if err := tcs3400.Identify(bg, r.bus); err != nil {
		t.Fatal(err)
	}
	if w := writes(r.bus); len(w) != 0 {
		t.Fatalf("Identify wrote:\n%s", describe(w))
	}
}

// ATIME = 256 - cycles; AGAIN codes 1x 00, 4x 01, 16x 10, 64x 11 (pp. 15, 18).
func TestConfigurationWritesTheDatasheetCodes(t *testing.T) {
	for gain, code := range map[tcs3400.Gain]uint8{tcs3400.Gain1: 0, tcs3400.Gain4: 1, tcs3400.Gain16: 2, tcs3400.Gain64: 3} {
		r := newRig(t)
		newSensor(t, r, tcs3400.WithGain(gain), tcs3400.WithIntegration(10))
		if got := r.m.Peek(0x8F); got != code {
			t.Errorf("gain %d: CONTROL = %#x, want %#x", gain, got, code)
		}
		if got := r.m.Peek(0x81); got != 0xF6 {
			t.Errorf("10 cycles: ATIME = %#x, want 0xf6", got)
		}
		if got := r.m.Peek(0x80); got != 0 {
			t.Errorf("ENABLE after New = %#x, want the chip asleep", got)
		}
	}
	r := newRig(t)
	newSensor(t, r)
	if r.m.Peek(0x81) != 0xC0 || r.m.Peek(0x8F) != 0b10 {
		t.Errorf("defaults: ATIME %#x CONTROL %#x, want 0xc0 (178 ms) and 16x", r.m.Peek(0x81), r.m.Peek(0x8F))
	}
}

func TestReadTakesOneIntegrationAndSleepsAgain(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.m.Set(3215, 736, 1463, 1408)
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	src := vane.Source{Chip: vane.ChipTCS3400, Bus: "/dev/i2c-1", Address: 0x39}
	for name, c := range map[string]struct {
		m    vane.Measurement[vane.Counts]
		want float64
	}{"clear": {got.Clear, 3215}, "red": {got.Red, 736}, "green": {got.Green, 1463}, "blue": {got.Blue, 1408}} {
		if float64(c.m.Value) != c.want || c.m.Raw != int32(c.want) || c.m.Source != src || !c.m.Time.Equal(fixedTime) || c.m.Flags != 0 {
			t.Errorf("%s = %+v, want %v from %+v", name, c.m, c.want, src)
		}
	}
	if got.Gain != tcs3400.Gain16 || got.IntegrationTime != 64*2780*time.Microsecond {
		t.Errorf("gain %d integration %v, want 16 and 177.92ms", got.Gain, got.IntegrationTime)
	}
	if r.m.Peek(0x80) != 0 {
		t.Errorf("ENABLE after Read = %#x, want the chip asleep", r.m.Peek(0x80))
	}
	// Every Read is a new integration: new counts arrive.
	r.m.Set(120, 57, 66, 60)
	if got, _ := s.Read(bg); got.Clear.Value != 120 {
		t.Errorf("second Read clear = %v, want 120: it returned the old sample", got.Clear.Value)
	}
}

// Negative control: dropping the ASAT check leaves the first case unflagged;
// dropping the ceiling check leaves the second.
func TestSaturationIsFlagged(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, tcs3400.WithIntegration(1)) // max count 1024
	r.m.Set(5000, 10, 10, 10)                        // above the ceiling: clipped, ASAT
	got, _ := s.Read(bg)
	if got.Clear.Value != 1024 || !got.Clear.Flags.Has(vane.FlagClamped) || !got.Red.Flags.Has(vane.FlagClamped) {
		t.Errorf("analog saturation: clear %v flags %v, red flags %v, want 1024 and FlagClamped on every channel", got.Clear.Value, got.Clear.Flags, got.Red.Flags)
	}
	r.m.Set(1024, 10, 10, 10) // at the ceiling exactly: no ASAT from the model
	got, _ = s.Read(bg)
	if !got.Clear.Flags.Has(vane.FlagClamped) || got.Red.Flags.Has(vane.FlagClamped) {
		t.Errorf("digital saturation: clear flags %v red flags %v, want only clear flagged", got.Clear.Flags, got.Red.Flags)
	}
}

func TestACycleThatNeverCompletesIsNotReadyAndSleeps(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(1_000_000))
	s := newSensor(t, r, tcs3400.WithReadyTimeout(2*time.Millisecond))
	if _, err := s.Read(bg); !errors.Is(err, vane.ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if r.m.Peek(0x80) != 0 {
		t.Errorf("ENABLE after a timeout = %#x, want the chip asleep", r.m.Peek(0x80))
	}
}

// A reset puts ATIME back to 0xFF (2.78 ms): without the check the next Read
// integrates at the wrong scale with no error. Negative control: skipping the
// read-back leaves ATIME at 0xff.
func TestAResetChipIsReconfiguredBeforeTheNextIntegration(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	r.m.Reset()
	r.m.Set(20000, 1, 1, 1)
	got, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	if r.m.Peek(0x81) != 0xC0 || r.m.Peek(0x8F) != 0b10 || got.Clear.Value != 20000 {
		t.Errorf("after a reset: ATIME %#x CONTROL %#x clear %v, want 0xc0, 16x and 20000 (not clipped at 1024)", r.m.Peek(0x81), r.m.Peek(0x8F), got.Clear.Value)
	}
}

func TestInvalidOptionsTouchNothing(t *testing.T) {
	for name, opt := range map[string]tcs3400.Option{
		"0 cycles":   tcs3400.WithIntegration(0),
		"257 cycles": tcs3400.WithIntegration(257),
		"gain 2":     tcs3400.WithGain(2),
		"address":    tcs3400.WithAddress(0x80),
		"nil clock":  tcs3400.WithClock(nil),
		"poll 0":     tcs3400.WithPollInterval(0),
	} {
		r := newRig(t)
		if _, err := tcs3400.New(bg, r.bus, opt); err == nil {
			t.Errorf("%s accepted", name)
		}
		if n := len(r.bus.Log()); n != 0 {
			t.Errorf("%s: %d transactions", name, n)
		}
	}
}

func TestCloseSleepsAndIsIdempotent(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r)
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	n := len(r.bus.Log())
	if err := s.Close(bg); err != nil || len(r.bus.Log()) != n {
		t.Errorf("second Close: %v, %d new transactions", err, len(r.bus.Log())-n)
	}
	if _, err := s.Read(bg); !errors.Is(err, vane.ErrClosed) {
		t.Errorf("Read after Close: %v", err)
	}
}

func TestACancelledContextTouchesNothing(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := tcs3400.New(ctx, r.bus); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if n := len(r.bus.Log()); n != 0 {
		t.Errorf("%d transactions", n)
	}
}

type directBus struct{ m *vanetest.TCS3400Model }

func (b directBus) Tx(_ context.Context, _ uint16, w, r []byte) error { return b.m.Tx(w, r) }

// RNF-08, at the driver level.
func TestReadDoesNotAllocate(t *testing.T) {
	b := directBus{vanetest.NewTCS3400(vanetest.WithConversionPolls(1))}
	s, err := tcs3400.New(bg, b, tcs3400.WithPollInterval(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(100, func() { _, _ = s.Read(bg) }); n != 0 {
		t.Errorf("Read allocates %v times per call, want 0", n)
	}
}

// A context that ends during the wait still leaves the chip asleep: the ADC off
// write uses a context that does not end with the caller's. Negative control:
// stopping with the caller's context leaves ENABLE at 0x03.
func TestAContextEndingDuringTheWaitStillLeavesTheChipAsleep(t *testing.T) {
	r := newRig(t, vanetest.WithConversionPolls(1_000_000))
	s := newSensor(t, r, tcs3400.WithReadyTimeout(time.Minute))
	ctx, cancel := context.WithTimeout(bg, 5*time.Millisecond)
	defer cancel()
	if _, err := s.Read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if got := r.m.Peek(0x80); got != 0 {
		t.Errorf("ENABLE = %#x after the context ended, want the chip asleep", got)
	}
}
