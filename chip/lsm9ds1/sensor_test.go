package lsm9ds1_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lsm9ds1"
	"github.com/JonasBorgesLM/vane/vanetest"
)

var (
	bg        = context.Background()
	fixedTime = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
)

type reporter struct{ t *testing.T }

func (r *reporter) Errorf(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf("protocol violation: "+format, args...)
}

type rig struct {
	ag  *vanetest.LSM9DS1AccelGyroModel
	mag *vanetest.LSM9DS1MagnetometerModel
	bus *vanetest.FakeBus
}

func newRig(t *testing.T, agOpts, magOpts []vanetest.ModelOption) *rig {
	t.Helper()
	rep := vanetest.WithReporter(&reporter{t: t})
	ag := vanetest.NewLSM9DS1AccelGyro(append([]vanetest.ModelOption{rep}, agOpts...)...)
	mag := vanetest.NewLSM9DS1Magnetometer(append([]vanetest.ModelOption{rep}, magOpts...)...)
	return &rig{ag: ag, mag: mag, bus: vanetest.NewFakeBus(ag, mag)}
}

func newSensor(t *testing.T, r *rig, opts ...lsm9ds1.Option) *lsm9ds1.Sensor {
	t.Helper()
	s, err := lsm9ds1.New(bg, r.bus, append([]lsm9ds1.Option{lsm9ds1.WithBusName("/dev/i2c-1"), lsm9ds1.WithClock(func() time.Time { return fixedTime })}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func describe(log []vanetest.Tx) string {
	var s string
	for i, tx := range log {
		s += fmt.Sprintf("  %d: addr=%#x w=%x r=%x err=%v\n", i, tx.Addr, tx.W, tx.R, tx.Err)
	}
	return s
}

// RF-01, per device. Negative control: moving configureAccelGyro before its
// identify makes the first 0x6A transaction a write.
func TestEachDeviceIsIdentifiedBeforeItIsWritten(t *testing.T) {
	r := newRig(t, nil, nil)
	newSensor(t, r)
	for _, addr := range []uint16{0x6A, 0x1C} {
		for _, tx := range r.bus.Log() {
			if tx.Addr != addr {
				continue
			}
			if len(tx.W) != 1 || tx.W[0] != 0x0F || len(tx.R) != 1 {
				t.Fatalf("the first transaction to 0x%02x is not its WHO_AM_I read:\n%s", addr, describe(r.bus.Log()))
			}
			break
		}
	}
}

func TestConfigurationWritesBDUAndRunsBothDevices(t *testing.T) {
	r := newRig(t, nil, nil)
	newSensor(t, r, lsm9ds1.WithRate(lsm9ds1.Rate238Hz), lsm9ds1.WithAccelRange(lsm9ds1.Accel8G), lsm9ds1.WithGyroRange(lsm9ds1.Gyro2000DPS), lsm9ds1.WithMagRange(lsm9ds1.Mag12Gauss))
	for name, c := range map[string]struct {
		got, want uint8
	}{
		"CTRL_REG8 (BDU, IF_ADD_INC)":    {r.ag.Peek(0x22), 0x44},
		"CTRL_REG1_G (238 Hz, 2000 dps)": {r.ag.Peek(0x10), 4<<5 | 0b11<<3},
		"CTRL_REG6_XL (238 Hz, 8 g)":     {r.ag.Peek(0x20), 4<<5 | 0b11<<3},
		"FIFO_CTRL (Bypass)":             {r.ag.Peek(0x2E), 0},
		"CTRL_REG5_M (BDU)":              {r.mag.Peek(0x24), 0x40},
		"CTRL_REG2_M (12 gauss)":         {r.mag.Peek(0x21), 0b10 << 5},
		"CTRL_REG1_M (10 Hz)":            {r.mag.Peek(0x20), 0x10},
		"CTRL_REG3_M (continuous)":       {r.mag.Peek(0x22), 0x00},
	} {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", name, c.got, c.want)
		}
	}
}

// Sensitivities from DocID025715 Rev 3 Table 3, repeated here, not taken from
// the driver: mg, mdps and mgauss per LSB.
func TestReadConvertsWithTheFullScaleSensitivity(t *testing.T) {
	for _, tc := range []struct {
		accel          lsm9ds1.AccelRange
		gyro           lsm9ds1.GyroRange
		mgLSB, mdpsLSB float64
	}{
		{lsm9ds1.Accel2G, lsm9ds1.Gyro245DPS, 0.061, 8.75},
		{lsm9ds1.Accel4G, lsm9ds1.Gyro500DPS, 0.122, 17.5},
		{lsm9ds1.Accel8G, lsm9ds1.Gyro2000DPS, 0.244, 70},
		{lsm9ds1.Accel16G, lsm9ds1.Gyro245DPS, 0.732, 8.75},
	} {
		t.Run(fmt.Sprintf("%dg/%ddps", tc.accel, tc.gyro), func(t *testing.T) {
			r := newRig(t, nil, nil)
			s := newSensor(t, r, lsm9ds1.WithAccelRange(tc.accel), lsm9ds1.WithGyroRange(tc.gyro))
			r.ag.Set(vanetest.Axes{1000, -2000, 3}, vanetest.Axes{16393, -100, 0})
			r.ag.Tick()
			m, err := s.Read(bg)
			if err != nil {
				t.Fatal(err)
			}
			src := vane.Source{Chip: vane.ChipLSM9DS1, Bus: "/dev/i2c-1", Address: 0x6A}
			for name, c := range map[string]struct {
				got  float64
				raw  int32
				want float64
				src  vane.Source
			}{
				"gyro X":  {float64(m.AngularRate.X.Value), m.AngularRate.X.Raw, 1000 * tc.mdpsLSB / 1000, m.AngularRate.X.Source},
				"gyro Y":  {float64(m.AngularRate.Y.Value), m.AngularRate.Y.Raw, -2000 * tc.mdpsLSB / 1000, m.AngularRate.Y.Source},
				"accel X": {float64(m.Acceleration.X.Value), m.Acceleration.X.Raw, 16393 * tc.mgLSB / 1000, m.Acceleration.X.Source},
				"accel Y": {float64(m.Acceleration.Y.Value), m.Acceleration.Y.Raw, -100 * tc.mgLSB / 1000, m.Acceleration.Y.Source},
			} {
				if math.Abs(c.got-c.want) > 1e-9 || c.src != src {
					t.Errorf("%s = %v (raw %d, %+v), want %v from %+v", name, c.got, c.raw, c.src, c.want, src)
				}
			}
			if !m.AngularRate.Z.Time.Equal(fixedTime) || m.Acceleration.X.Flags != 0 {
				t.Errorf("time %v flags %v", m.AngularRate.Z.Time, m.Acceleration.X.Flags)
			}
		})
	}
}

func TestReadFlagsStaleAndSaturatedAxes(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r)
	r.ag.Set(vanetest.Axes{math.MaxInt16, 0, math.MinInt16}, vanetest.Axes{1, 2, 3})
	r.ag.Tick()
	m, err := s.Read(bg)
	if err != nil {
		t.Fatal(err)
	}
	if !m.AngularRate.X.Flags.Has(vane.FlagClamped) || !m.AngularRate.Z.Flags.Has(vane.FlagClamped) || m.AngularRate.Y.Flags.Has(vane.FlagClamped) {
		t.Errorf("clamped flags: X %v Y %v Z %v", m.AngularRate.X.Flags, m.AngularRate.Y.Flags, m.AngularRate.Z.Flags)
	}
	m, err = s.Read(bg) // no Tick: nothing new
	if err != nil {
		t.Fatal(err)
	}
	if !m.AngularRate.Y.Flags.Has(vane.FlagStale) || !m.Acceleration.Z.Flags.Has(vane.FlagStale) {
		t.Errorf("a second read without a new sample is not stale: %v %v", m.AngularRate.Y.Flags, m.Acceleration.Z.Flags)
	}
}

func TestReadMagneticConvertsAndFlags(t *testing.T) {
	for _, tc := range []struct {
		rng   lsm9ds1.MagRange
		mgLSB float64
	}{{lsm9ds1.Mag4Gauss, 0.14}, {lsm9ds1.Mag8Gauss, 0.29}, {lsm9ds1.Mag12Gauss, 0.43}, {lsm9ds1.Mag16Gauss, 0.58}} {
		r := newRig(t, nil, nil)
		s := newSensor(t, r, lsm9ds1.WithMagRange(tc.rng))
		r.mag.Set(vanetest.Axes{2000, -500, 7})
		r.mag.Tick()
		v, err := s.ReadMagnetic(bg)
		if err != nil {
			t.Fatal(err)
		}
		if want := 2000 * tc.mgLSB / 1000; math.Abs(float64(v.X.Value)-want) > 1e-9 || v.X.Source.Address != 0x1C || v.X.Flags != 0 {
			t.Errorf("±%d gauss: X = %v (%+v, flags %v), want %v from 0x1c", tc.rng, v.X.Value, v.X.Source, v.X.Flags, want)
		}
	}
	r := newRig(t, nil, nil)
	s := newSensor(t, r)
	r.mag.Tick()
	r.mag.Tick() // a second sample over an unread one
	v, _ := s.ReadMagnetic(bg)
	if !v.Y.Flags.Has(vane.FlagOverrun) {
		t.Errorf("an overwritten sample is not flagged: %v", v.Y.Flags)
	}
	if v, _ := s.ReadMagnetic(bg); !v.Z.Flags.Has(vane.FlagStale) {
		t.Errorf("a second read without a new sample is not stale: %v", v.Z.Flags)
	}
}

// ADR-0006: one device failing leaves the other working, and nothing is written
// to the one that did not identify.
func TestAMagnetometerThatDoesNotIdentifyLeavesTheRestWorking(t *testing.T) {
	r := newRig(t, nil, []vanetest.ModelOption{vanetest.WithWhoAmI(0x00)})
	s := newSensor(t, r)
	for _, tx := range r.bus.Log() {
		if tx.Addr == 0x1C && len(tx.W) > 1 {
			t.Fatalf("a write reached a magnetometer that did not identify:\n%s", describe(r.bus.Log()))
		}
	}
	r.ag.Tick()
	if _, err := s.Read(bg); err != nil {
		t.Errorf("Read: %v, want the accelerometer and gyroscope to work", err)
	}
	_, err := s.ReadMagnetic(bg)
	var ie *vane.IdentityError
	if !errors.As(err, &ie) || !errors.Is(err, vane.ErrUnexpectedChip) || ie.Source.Address != 0x1C {
		t.Errorf("ReadMagnetic: %v, want the magnetometer's identity error", err)
	}
	if err := s.Close(bg); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestAnAccelGyroThatIsMissingLeavesTheMagnetometerWorking(t *testing.T) {
	mag := vanetest.NewLSM9DS1Magnetometer(vanetest.WithReporter(&reporter{t: t}))
	s, err := lsm9ds1.New(bg, vanetest.NewFakeBus(mag))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(bg); !errors.Is(err, vane.ErrNotPresent) {
		t.Errorf("Read: %v, want ErrNotPresent", err)
	}
	mag.Tick()
	if _, err := s.ReadMagnetic(bg); err != nil {
		t.Errorf("ReadMagnetic: %v", err)
	}
}

func TestNewFailsWhenNeitherDeviceIsThere(t *testing.T) {
	_, err := lsm9ds1.New(bg, vanetest.NewFakeBus())
	if !errors.Is(err, vane.ErrNotPresent) {
		t.Fatalf("err = %v, want ErrNotPresent for both", err)
	}
}

func TestInvalidOptionsTouchNothing(t *testing.T) {
	for name, opt := range map[string]lsm9ds1.Option{
		"rate 0":        lsm9ds1.WithRate(0),
		"rate 7":        lsm9ds1.WithRate(7),
		"accel 3 g":     lsm9ds1.WithAccelRange(3),
		"gyro 1000 dps": lsm9ds1.WithGyroRange(1000),
		"mag 5 gauss":   lsm9ds1.WithMagRange(5),
		"same address":  lsm9ds1.WithMagnetometerAddress(0x6A),
		"nil clock":     lsm9ds1.WithClock(nil),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, nil, nil)
			if _, err := lsm9ds1.New(bg, r.bus, opt); err == nil {
				t.Fatal("accepted")
			}
			if n := len(r.bus.Log()); n != 0 {
				t.Errorf("%d transactions before the option was refused", n)
			}
		})
	}
}

func TestClosePowersBothDownAndIsIdempotent(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r)
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if g, xl, m := r.ag.Peek(0x10), r.ag.Peek(0x20), r.mag.Peek(0x22); g != 0 || xl != 0 || m&0x03 != 0x03 {
		t.Errorf("after Close: CTRL_REG1_G %#x, CTRL_REG6_XL %#x, CTRL_REG3_M %#x; want both powered down", g, xl, m)
	}
	n := len(r.bus.Log())
	if err := s.Close(bg); err != nil || len(r.bus.Log()) != n {
		t.Errorf("a second Close: %v, %d new transactions", err, len(r.bus.Log())-n)
	}
	if _, err := s.Read(bg); !errors.Is(err, vane.ErrClosed) {
		t.Errorf("Read after Close: %v", err)
	}
	if _, err := s.ReadMagnetic(bg); !errors.Is(err, vane.ErrClosed) {
		t.Errorf("ReadMagnetic after Close: %v", err)
	}
}

func TestACancelledContextTouchesNothing(t *testing.T) {
	r := newRig(t, nil, nil)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := lsm9ds1.New(ctx, r.bus); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if n := len(r.bus.Log()); n != 0 {
		t.Errorf("%d transactions under a cancelled context", n)
	}
}

// directBus hands transactions straight to the models, with none of FakeBus's
// logging, so what is measured is the driver and not the test double.
type directBus struct {
	ag  *vanetest.LSM9DS1AccelGyroModel
	mag *vanetest.LSM9DS1MagnetometerModel
}

func (b directBus) Tx(_ context.Context, addr uint16, w, r []byte) error {
	if addr == 0x6A {
		return b.ag.Tx(w, r)
	}
	return b.mag.Tx(w, r)
}

// RNF-08: no allocation per read at the driver level.
func TestReadDoesNotAllocate(t *testing.T) {
	b := directBus{vanetest.NewLSM9DS1AccelGyro(), vanetest.NewLSM9DS1Magnetometer()}
	s, err := lsm9ds1.New(bg, b)
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func(){
		"Read":         func() { _, _ = s.Read(bg) },
		"ReadMagnetic": func() { _, _ = s.ReadMagnetic(bg) },
	} {
		if n := testing.AllocsPerRun(100, fn); n != 0 {
			t.Errorf("%s allocates %v times per call, want 0", name, n)
		}
	}
}

// The register code of every full scale, from DocID025715 Rev 3 (pp. 45, 52,
// 64), repeated here: the accelerometer's codes are not in order (±16 g is 01),
// and a driver that wrote the wrong code while converting with the right
// sensitivity would pass every conversion test.
func TestEachFullScaleWritesItsDatasheetCode(t *testing.T) {
	for rng, code := range map[lsm9ds1.AccelRange]uint8{lsm9ds1.Accel2G: 0b00, lsm9ds1.Accel16G: 0b01, lsm9ds1.Accel4G: 0b10, lsm9ds1.Accel8G: 0b11} {
		r := newRig(t, nil, nil)
		newSensor(t, r, lsm9ds1.WithAccelRange(rng))
		if got := r.ag.Peek(0x20) >> 3 & 0b11; got != code {
			t.Errorf("±%d g: FS_XL = %02b, want %02b", rng, got, code)
		}
	}
	for rng, code := range map[lsm9ds1.GyroRange]uint8{lsm9ds1.Gyro245DPS: 0b00, lsm9ds1.Gyro500DPS: 0b01, lsm9ds1.Gyro2000DPS: 0b11} {
		r := newRig(t, nil, nil)
		newSensor(t, r, lsm9ds1.WithGyroRange(rng))
		if got := r.ag.Peek(0x10) >> 3 & 0b11; got != code {
			t.Errorf("±%d dps: FS_G = %02b, want %02b", rng, got, code)
		}
	}
	for rng, code := range map[lsm9ds1.MagRange]uint8{lsm9ds1.Mag4Gauss: 0b00, lsm9ds1.Mag8Gauss: 0b01, lsm9ds1.Mag12Gauss: 0b10, lsm9ds1.Mag16Gauss: 0b11} {
		r := newRig(t, nil, nil)
		newSensor(t, r, lsm9ds1.WithMagRange(rng))
		if got := r.mag.Peek(0x21) >> 5 & 0b11; got != code {
			t.Errorf("±%d gauss: FS = %02b, want %02b", rng, got, code)
		}
	}
	for rate, code := range map[lsm9ds1.Rate]uint8{lsm9ds1.Rate14Hz9: 1, lsm9ds1.Rate59Hz5: 2, lsm9ds1.Rate119Hz: 3, lsm9ds1.Rate238Hz: 4, lsm9ds1.Rate476Hz: 5, lsm9ds1.Rate952Hz: 6} {
		r := newRig(t, nil, nil)
		newSensor(t, r, lsm9ds1.WithRate(rate))
		if got := r.ag.Peek(0x10) >> 5; got != code {
			t.Errorf("rate %d: ODR_G = %03b, want %03b", rate, got, code)
		}
	}
}
