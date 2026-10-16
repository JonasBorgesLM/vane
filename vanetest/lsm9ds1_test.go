package vanetest_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

type ag struct {
	t   *testing.T
	m   *vanetest.LSM9DS1AccelGyroModel
	rec *recorder
}

func newAG(t *testing.T) *ag {
	rec := &recorder{}
	return &ag{t: t, m: vanetest.NewLSM9DS1AccelGyro(vanetest.WithReporter(rec)), rec: rec}
}

func (a *ag) write(reg uint8, v byte) {
	a.t.Helper()
	if err := a.m.Tx([]byte{reg, v}, nil); err != nil {
		a.t.Fatalf("write 0x%02x = %#x: %v", reg, v, err)
	}
}

func (a *ag) read(reg uint8, n int) []byte {
	a.t.Helper()
	r := make([]byte, n)
	if err := a.m.Tx([]byte{reg}, r); err != nil {
		a.t.Fatalf("read 0x%02x x%d: %v", reg, n, err)
	}
	return r
}

// on powers the gyroscope and accelerometer at 119 Hz with BDU and IF_ADD_INC.
func (a *ag) on() {
	a.t.Helper()
	a.write(0x22, 0x44)
	a.write(0x10, 0x60)
	a.write(0x20, 0x60)
}

func (a *ag) fifo(mode byte) {
	a.t.Helper()
	a.write(0x23, 0x02)
	a.write(0x2E, mode<<5)
}

func le(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8) // #nosec G115 -- reinterpreting two's complement is the point
	}
	return out
}

func sample(n int16) (g, xl vanetest.Axes) {
	return vanetest.Axes{n, n + 1, n + 2}, vanetest.Axes{-n, -n - 1, -n - 2}
}

func TestLSM9DS1AccelGyroPowerOnState(t *testing.T) {
	a := newAG(t)
	for reg, want := range map[uint8]byte{0x0F: 0x68, 0x22: 0x04, 0x1E: 0x38, 0x1F: 0x38, 0x10: 0, 0x20: 0, 0x2E: 0, 0x2F: 0} {
		if got := a.read(reg, 1)[0]; got != want {
			t.Errorf("register 0x%02x = %#x, want %#x", reg, got, want)
		}
	}
}

// On the board, 0x8F read 0x00 instead of WHO_AM_I.
func TestLSM9DS1AccelGyroRefusesTheSubAddressMSB(t *testing.T) {
	a := newAG(t)
	if err := a.m.Tx([]byte{0x8F}, make([]byte, 1)); !errors.Is(err, vanetest.ErrProtocol) {
		t.Fatalf("0x8F: err = %v, want a protocol violation", err)
	}
	// The specific rule, not just any violation: 0x8F is also past 0x7F, and a
	// check that only caught that would pass this test for the wrong reason.
	if len(a.rec.msgs) != 1 || !strings.Contains(a.rec.msgs[0], "bit 7 set") {
		t.Errorf("reported %q, want the bit-7 rule", a.rec.msgs)
	}
}

func TestLSM9DS1AccelGyroBlockReadNeedsIfAddInc(t *testing.T) {
	a := newAG(t)
	a.m.Poke(0x22, 0x40) // BDU on, IF_ADD_INC off, bypassing the write rules
	if err := a.m.Tx([]byte{0x18}, make([]byte, 6)); !errors.Is(err, vanetest.ErrProtocol) {
		t.Fatalf("err = %v, want a protocol violation", err)
	}
}

// On the board: a 12-byte burst from OUT_X_G read the gyroscope then the
// accelerometer, skipping the control registers between them; 24 bytes wrapped
// back to OUT_X_G; from OUT_X_XL it wrapped to OUT_X_G.
func TestLSM9DS1AccelGyroBurstWalksGyroThenAccelAndWraps(t *testing.T) {
	a := newAG(t)
	a.on()
	g, xl := sample(100)
	a.m.Set(g, xl)
	a.m.Tick()
	want := append(slices.Clone(g[:]), xl[:]...)
	if got := le(a.read(0x18, 12)); !slices.Equal(got, want) {
		t.Fatalf("12 bytes from 0x18 = %v, want %v", got, want)
	}
	if got := le(a.read(0x18, 24)); !slices.Equal(got, append(slices.Clone(want), want...)) {
		t.Fatalf("24 bytes from 0x18 = %v, want the sample twice", got)
	}
	if got := le(a.read(0x28, 12)); !slices.Equal(got, append(slices.Clone(xl[:]), g[:]...)) {
		t.Fatalf("12 bytes from 0x28 = %v, want accelerometer then gyroscope", got)
	}
}

func TestLSM9DS1AccelGyroAccelOnlyBurstWrapsOnItself(t *testing.T) {
	a := newAG(t)
	a.write(0x22, 0x44)
	a.write(0x20, 0x60) // accelerometer only
	_, xl := sample(7)
	a.m.Set(vanetest.Axes{}, xl)
	a.m.Tick()
	if got := le(a.read(0x28, 12)); !slices.Equal(got, append(slices.Clone(xl[:]), xl[:]...)) {
		t.Fatalf("got %v, want the accelerometer twice", got)
	}
}

// On the board: in Continuous mode, after 0.5 s FIFO_SRC read 0x60 (32 unread,
// OVRN); one 12-byte burst made it 0x1F; three samples read in one 36-byte
// burst were distinct.
func TestLSM9DS1FIFOContinuousOverrunsAndDrainsOldestFirst(t *testing.T) {
	a := newAG(t)
	a.on()
	a.fifo(6)
	for n := range int16(40) {
		a.m.Set(sample(n * 10))
		a.m.Tick()
	}
	if got := a.read(0x2F, 1)[0]; got != 0x60 {
		t.Fatalf("FIFO_SRC after 40 samples = %#x, want 0x60 (full, overrun)", got)
	}
	// The 8 oldest were overwritten: the first one left is sample 8.
	got := le(a.read(0x18, 36))
	for i := range 3 {
		g, xl := sample(int16(8+i) * 10)
		if want := append(slices.Clone(g[:]), xl[:]...); !slices.Equal(got[6*i:6*i+6], want) {
			t.Fatalf("sample %d = %v, want %v", i, got[6*i:6*i+6], want)
		}
	}
	if got := a.read(0x2F, 1)[0]; got != 29 {
		t.Fatalf("FIFO_SRC after three samples read = %#x, want 29 unread, OVRN clear", got)
	}
}

// On the board: six gyroscope bytes alone left FSS at 6; the six accelerometer
// bytes then made it 5.
func TestLSM9DS1FIFOSampleLeavesWhenTheAccelerometerIsRead(t *testing.T) {
	a := newAG(t)
	a.on()
	a.fifo(6)
	for range 6 {
		a.m.Tick()
	}
	a.read(0x18, 6)
	if got := a.m.Buffered(); got != 6 {
		t.Fatalf("after the gyroscope bytes: %d buffered, want 6", got)
	}
	a.read(0x28, 6)
	if got := a.m.Buffered(); got != 5 {
		t.Fatalf("after the accelerometer bytes: %d buffered, want 5", got)
	}
}

// On the board, FIFO mode stopped at 32 and FIFO_SRC read 0x60.
func TestLSM9DS1FIFOModeStopsWhenFull(t *testing.T) {
	a := newAG(t)
	a.on()
	a.fifo(1)
	for n := range int16(40) {
		a.m.Set(sample(n))
		a.m.Tick()
	}
	if got := a.read(0x2F, 1)[0]; got != 0x60 {
		t.Fatalf("FIFO_SRC = %#x, want 0x60", got)
	}
	g, xl := sample(0)
	if got := le(a.read(0x18, 12)); !slices.Equal(got, append(slices.Clone(g[:]), xl[:]...)) {
		t.Fatalf("oldest = %v, want sample 0: FIFO mode keeps the first 32", got)
	}
}

func TestLSM9DS1FIFOBypassEmptiesIt(t *testing.T) {
	a := newAG(t)
	a.on()
	a.fifo(6)
	for range 10 {
		a.m.Tick()
	}
	a.write(0x2E, 0x00)
	if got := a.read(0x2F, 1)[0]; got != 0 {
		t.Fatalf("FIFO_SRC after Bypass = %#x, want 0", got)
	}
}

func TestLSM9DS1FIFOThresholdAndStopOnFTH(t *testing.T) {
	a := newAG(t)
	a.on()
	a.write(0x23, 0x03)      // FIFO_EN, STOP_ON_FTH
	a.write(0x2E, 6<<5|0x07) // Continuous, FTH 7
	for range 20 {
		a.m.Tick()
	}
	if got := a.read(0x2F, 1)[0]; got != 1<<7|1<<6|8 {
		t.Fatalf("FIFO_SRC = %#x, want FTH, OVRN and 8 unread (depth FTH+1)", got)
	}
}

func TestLSM9DS1AccelGyroDataReadyWithoutFIFO(t *testing.T) {
	a := newAG(t)
	a.on()
	a.m.Tick()
	if got := a.read(0x27, 1)[0] & 0x03; got != 0x03 {
		t.Fatalf("STATUS_REG = %#x, want GDA and XLDA", got)
	}
	a.read(0x18, 12)
	if got := a.read(0x27, 1)[0] & 0x03; got != 0 {
		t.Fatalf("STATUS_REG after reading both = %#x, want them clear", got)
	}
}

func TestLSM9DS1AccelGyroRefusesWhatVaneNeverDoes(t *testing.T) {
	for name, w := range map[string][]byte{
		"BDU off":           {0x22, 0x04},
		"IF_ADD_INC off":    {0x22, 0x40},
		"SW_RESET":          {0x22, 0x45},
		"BLE":               {0x22, 0x46},
		"reserved FMODE":    {0x2E, 2 << 5},
		"CTRL_REG9 extra":   {0x23, 0x10},
		"unwritable 0x1E":   {0x1E, 0x38},
		"read-only 0x0F":    {0x0F, 0x00},
		"read-only status":  {0x27, 0x00},
		"multi without inc": {0x10, 0x60, 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAG(t)
			if name == "multi without inc" {
				a.m.Poke(0x22, 0x40)
			}
			if err := a.m.Tx(w, nil); !errors.Is(err, vanetest.ErrProtocol) {
				t.Fatalf("err = %v, want a protocol violation", err)
			}
		})
	}
}

func TestLSM9DS1MagnetometerPowerOnState(t *testing.T) {
	m := vanetest.NewLSM9DS1Magnetometer()
	for reg, want := range map[uint8]byte{0x0F: 0x3D, 0x20: 0x10, 0x22: 0x03, 0x24: 0x00} {
		r := make([]byte, 1)
		if err := m.Tx([]byte{reg}, r); err != nil || r[0] != want {
			t.Errorf("register 0x%02x = %#x (%v), want %#x", reg, r[0], err, want)
		}
	}
}

func TestLSM9DS1MagnetometerContinuousAndSingle(t *testing.T) {
	m := vanetest.NewLSM9DS1Magnetometer()
	w := func(reg, v byte) {
		t.Helper()
		if err := m.Tx([]byte{reg, v}, nil); err != nil {
			t.Fatal(err)
		}
	}
	read := func() ([]int16, byte) {
		t.Helper()
		r := make([]byte, 6)
		if err := m.Tx([]byte{0x28 | 0x80}, r); err != nil {
			t.Fatal(err)
		}
		st := make([]byte, 1)
		_ = m.Tx([]byte{0x27}, st)
		return le(r), st[0]
	}
	w(0x24, 0x40) // BDU
	m.Tick()
	if v, _ := read(); !slices.Equal(v, []int16{0, 0, 0}) {
		t.Fatalf("a powered-down magnetometer sampled: %v", v)
	}
	w(0x22, 0x00) // continuous
	m.Set(vanetest.Axes{1, -2, 3})
	m.Tick()
	st := make([]byte, 1)
	_ = m.Tx([]byte{0x27}, st)
	if st[0]&0x08 == 0 {
		t.Fatalf("STATUS_REG_M = %#x, want ZYXDA", st[0])
	}
	if v, after := read(); !slices.Equal(v, []int16{1, -2, 3}) || after != 0 {
		t.Fatalf("got %v, status after %#x", v, after)
	}
	w(0x22, 0x01) // single
	m.Set(vanetest.Axes{9, 9, 9})
	m.Tick()
	cr3 := make([]byte, 1)
	_ = m.Tx([]byte{0x22}, cr3)
	if cr3[0]&0x03 != 0x03 {
		t.Fatalf("CTRL_REG3_M after a single conversion = %#x, want MD back to power-down", cr3[0])
	}
}

func TestLSM9DS1MagnetometerRefusesWhatVaneNeverDoes(t *testing.T) {
	for name, w := range map[string][]byte{
		"BDU off":     {0x24, 0x00},
		"SOFT_RST":    {0x21, 0x04},
		"REBOOT":      {0x21, 0x08},
		"I2C_DISABLE": {0x22, 0x83},
		"SIM":         {0x22, 0x07},
		"unwritable":  {0x05, 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			if err := vanetest.NewLSM9DS1Magnetometer().Tx(w, nil); !errors.Is(err, vanetest.ErrProtocol) {
				t.Fatalf("err = %v, want a protocol violation", err)
			}
		})
	}
	if err := vanetest.NewLSM9DS1Magnetometer().Tx([]byte{0x28}, make([]byte, 6)); !errors.Is(err, vanetest.ErrProtocol) {
		t.Fatalf("a block read without the MSB: err = %v, want a protocol violation", err)
	}
}

func TestLSM9DS1BothDevicesShareOneBus(t *testing.T) {
	bus := vanetest.NewFakeBus(vanetest.NewLSM9DS1AccelGyro(), vanetest.NewLSM9DS1Magnetometer())
	for addr, want := range map[uint16]byte{0x6A: 0x68, 0x1C: 0x3D} {
		r := make([]byte, 1)
		if err := bus.Tx(t.Context(), addr, []byte{0x0F}, r); err != nil || r[0] != want {
			t.Errorf("0x%02x WHO_AM_I = %#x (%v), want %#x", addr, r[0], err, want)
		}
	}
}
