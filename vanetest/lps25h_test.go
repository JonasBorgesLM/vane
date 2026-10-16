package vanetest_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

const (
	pDA = 1 << 1
	tOR = 1 << 4
	pOR = 1 << 5

	odr1Hz   = 1 << 4
	odr25Hz  = 4 << 4
	oneShotL = 0x84 // PD | BDU, ODR 0
)

type lps struct {
	t   *testing.T
	m   *vanetest.LPS25HModel
	rec *recorder
}

func newLPS(t *testing.T, opts ...vanetest.ModelOption) *lps {
	rec := &recorder{}
	m := vanetest.NewLPS25H(append([]vanetest.ModelOption{vanetest.WithReporter(rec)}, opts...)...)
	return &lps{t: t, m: m, rec: rec}
}

func (l *lps) write(reg uint8, vals ...byte) error {
	l.t.Helper()
	return l.m.Tx(append([]byte{reg}, vals...), nil)
}

func (l *lps) read(reg uint8, n int) []byte {
	l.t.Helper()
	w := reg
	if n > 1 {
		w |= 0x80
	}
	r := make([]byte, n)
	if err := l.m.Tx([]byte{w}, r); err != nil {
		l.t.Fatalf("read 0x%02x x%d: %v", reg, n, err)
	}
	return r
}

func (l *lps) status() byte { return l.read(0x27, 1)[0] }

// outputs reads the five output bytes and decodes them by hand: pressure as
// 24-bit two's complement, temperature as 16-bit.
func (l *lps) outputs() (press int32, temp int16) {
	b := l.read(0x28, 5)
	p := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
	if p&0x800000 != 0 {
		p -= 1 << 24
	}
	return p, int16(uint16(b[3]) | uint16(b[4])<<8) // #nosec G115 -- two's complement
}

func (l *lps) convert() {
	l.t.Helper()
	if err := l.write(0x20, oneShotL); err != nil {
		l.t.Fatal(err)
	}
	if err := l.write(0x21, oneShot); err != nil {
		l.t.Fatal(err)
	}
	_ = l.status()
}

func TestLPS25HIdentityAndPowerOnState(t *testing.T) {
	l := newLPS(t)
	if got := l.read(0x0F, 1)[0]; got != 0xBD {
		t.Errorf("WHO_AM_I = %#x, want 0xbd", got)
	}
	for reg, want := range map[uint8]byte{0x20: 0, 0x21: 0, 0x27: 0, 0x28: 0, 0x2B: 0, 0x10: 0x05} {
		if got := l.m.Peek(reg); got != want {
			t.Errorf("register 0x%02x at power-on = %#x, want %#x", reg, got, want)
		}
	}
	if l.m.Addr() != 0x5C {
		t.Errorf("address = %#x", l.m.Addr())
	}
	if got := newLPS(t, vanetest.WithWhoAmI(0xBC)).read(0x0F, 1)[0]; got != 0xBC {
		t.Errorf("WithWhoAmI: %#x", got)
	}
}

// Counts computed by hand from the datasheet's conversion run backwards: 4096
// per hPa; 480 per degree around 42.5 degC.
func TestLPS25HSetCountsAreTheInverseOfTheConversion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		p, t  float64
		wantP int32
		wantT int16
	}{
		{"standard atmosphere at the offset temperature", 1013.25, 42.5, 4150272, 0}, // 1013.25 * 4096; 42.5 is the zero of the temperature scale
		{"a room", 1013.25, 22.5, 4150272, -9600},                                    // 20 degrees below 42.5, 480 each
		{"warmer than the offset", 1000, 52.5, 4096000, 4800},
		{"zero pressure", 0, 42.5, 0, 0},
		{"a negative reading", -10, 42.5, -40960, 0},        // differential output goes below zero
		{"pressure saturates high", 3000, 42.5, 8388607, 0}, // 12288000 counts do not fit 24 bits
		{"pressure saturates low", -3000, 42.5, -8388608, 0},
		{"temperature saturates", 1013.25, -30, 4150272, -32768}, // 72.5 degrees below the offset is 34800 counts down
		{"temperature saturates high", 1013.25, 150, 4150272, 32767},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLPS(t)
			l.m.Set(vanetest.LPS25HPressure(tc.p), vanetest.LPS25HTemperature(tc.t))
			l.convert()
			gotP, gotT := l.outputs()
			if gotP != tc.wantP || gotT != tc.wantT {
				t.Errorf("Set(%v, %v): counts %d, %d; want %d, %d", tc.p, tc.t, gotP, gotT, tc.wantP, tc.wantT)
			}
		})
	}
}

// The bytes as they sit in the registers, low first: 4150272 is 0x3F5400, and
// -40960 is 0xFF6000 in 24 bits.
func TestLPS25HOutputLayout(t *testing.T) {
	l := newLPS(t)
	l.m.Set(1013.25, 22.5)
	l.convert()
	want := []byte{0x00, 0x54, 0x3F, 0x80, 0xDA} // pressure 00 54 3F; temperature -9600 = 0xDA80
	if got := l.read(0x28, 5); !bytes.Equal(got, want) {
		t.Errorf("output bytes = %x, want %x", got, want)
	}

	l = newLPS(t)
	l.m.Set(-10, 42.5)
	l.convert()
	if got := l.read(0x28, 3); !bytes.Equal(got, []byte{0x00, 0x60, 0xFF}) {
		t.Errorf("negative pressure bytes = %x, want 00 60 ff as 24-bit two's complement", got)
	}
}

func TestLPS25HOneShot(t *testing.T) {
	l := newLPS(t, vanetest.WithConversionPolls(2))
	l.m.Set(1000, 42.5)
	if err := l.write(0x20, oneShotL); err != nil {
		t.Fatal(err)
	}
	if err := l.write(0x21, oneShot); err != nil {
		t.Fatal(err)
	}
	if l.m.Peek(0x21)&oneShot == 0 {
		t.Fatal("ONE_SHOT cleared before the conversion ended")
	}
	for i := range 2 {
		if s := l.status(); s != 0 {
			t.Fatalf("status read %d = %#x, want not ready", i, s)
		}
	}
	if s := l.status(); s != pDA|tDA {
		t.Fatalf("status after the conversion = %#x", s)
	}
	if l.m.Peek(0x21)&oneShot != 0 {
		t.Error("ONE_SHOT still set after the conversion")
	}
	if p, _ := l.outputs(); p != 4096000 {
		t.Errorf("pressure counts = %d", p)
	}
	if s := l.status(); s != 0 {
		t.Errorf("status after reading every output byte = %#x, want the flags cleared", s)
	}
}

func TestLPS25HOneShotIsIgnoredInContinuousMode(t *testing.T) {
	l := newLPS(t)
	_ = l.write(0x20, pd|bdu|odr1Hz)
	_ = l.write(0x21, oneShot)
	for range 50 {
		if s := l.status(); s != 0 {
			t.Fatalf("a one-shot request completed (status %#x)", s)
		}
	}
	if l.m.Peek(0x21)&oneShot != 0 {
		t.Errorf("an ignored request left ONE_SHOT set")
	}
}

// Observed on a Pi (docs/hardware.md): the request is latched, not dropped.
func TestLPS25HOneShotWhilePoweredDownRunsOnPowerUp(t *testing.T) {
	l := newLPS(t)
	_ = l.write(0x20, bdu)
	_ = l.write(0x21, oneShot)
	for range 50 {
		if s := l.status(); s != 0 {
			t.Fatalf("a conversion ran while powered down (status %#x)", s)
		}
	}
	if l.m.Peek(0x21)&oneShot == 0 {
		t.Fatalf("ONE_SHOT written while powered down was dropped, want it latched")
	}
	_ = l.write(0x20, pd|bdu)
	for i := 0; l.status()&(pDA|tDA) != pDA|tDA; i++ {
		if i == 50 {
			t.Fatalf("the latched one-shot did not run on power-up")
		}
	}
	if l.m.Peek(0x21)&oneShot != 0 {
		t.Errorf("ONE_SHOT still set after the latched conversion")
	}
}

// The high byte of an output clears its flags (DocID023722 Rev 6 p. 31,
// DocID027112 Rev 4 p. 41; on the board, PRESS_OUT_H alone took STATUS_REG from
// 0x03 to 0x01, and from 0x33 to 0x11 in continuous mode). Negative control: the
// earlier model, which waited for every byte, fails the high-byte-alone half.
func TestLPS25HAQuantityClearsWhenTheHighByteOfItsOutputIsRead(t *testing.T) {
	l := newLPS(t)
	l.convert()
	l.read(0x28, 1)
	l.read(0x29, 1)
	if s := l.status(); s != pDA|tDA {
		t.Fatalf("after PRESS_OUT_XL and _L: status %#x, want both flags", s)
	}
	l.read(0x2A, 1)
	if s := l.status(); s != tDA {
		t.Fatalf("after PRESS_OUT_H: status %#x, want only T_DA", s)
	}
	l.read(0x2C, 1) // TEMP_OUT_H alone
	if s := l.status(); s != 0 {
		t.Fatalf("after TEMP_OUT_H alone: status %#x, want 0", s)
	}
}

func TestLPS25HTheHighByteAloneClearsTheOverrunBit(t *testing.T) {
	l := newLPS(t)
	l.convert()
	l.m.Poke(0x27, l.m.Peek(0x27)|pOR|tOR) // the chip raising them is not modelled
	l.read(0x2A, 1)
	if s := l.status(); s != tDA|tOR {
		t.Fatalf("after PRESS_OUT_H alone: status %#x, want T_DA and T_OR left", s)
	}
}

func TestLPS25HOverrunBitsClearWithTheirQuantity(t *testing.T) {
	l := newLPS(t)
	l.convert()
	l.m.Poke(0x27, l.m.Peek(0x27)|pOR|tOR) // the chip raising them is not modelled; a test sets them
	if s := l.status(); s != pDA|tDA|pOR|tOR {
		t.Fatalf("status = %#x", s)
	}
	l.read(0x28, 3)
	if s := l.status(); s != tDA|tOR {
		t.Fatalf("after the pressure bytes: status %#x, want only the temperature bits", s)
	}
	l.read(0x2B, 2)
	if s := l.status(); s != 0 {
		t.Fatalf("after every byte: status %#x", s)
	}
}

func TestLPS25HBlockDataUpdateHoldsAnUnreadSample(t *testing.T) {
	l := newLPS(t)
	l.m.Set(900, 40)
	_ = l.write(0x20, pd|bdu|odr1Hz)
	l.m.NewSample()
	l.m.Set(1100, 40)
	l.m.NewSample() // arrives while the first is unread: lost
	first, _ := l.outputs()
	l.m.NewSample() // after the read: kept
	second, _ := l.outputs()
	if first != 900*4096 || second != 1100*4096 {
		t.Errorf("samples = %d, %d; want %d, %d", first, second, 900*4096, 1100*4096)
	}
}

func TestLPS25HNewSampleOnlyInContinuousMode(t *testing.T) {
	l := newLPS(t)
	l.m.NewSample()
	_ = l.write(0x20, oneShotL)
	l.m.NewSample()
	if s := l.status(); s != 0 {
		t.Fatalf("status = %#x, want no sample outside continuous mode", s)
	}
}

func TestLPS25HEveryRateIsAccepted(t *testing.T) {
	for odr := byte(0); odr <= 4; odr++ {
		l := newLPS(t)
		if err := l.write(0x20, pd|bdu|odr<<4); err != nil {
			t.Errorf("ODR %d refused: %v", odr, err)
		}
	}
}

// Rules that are vane's. Negative control, seen failing: removing each case in
// checkWrite.
func TestLPS25HRefusesWhatVaneNeverDoes(t *testing.T) {
	tests := []struct {
		name string
		reg  uint8
		val  byte
	}{
		{"BDU off in a power-up", 0x20, pd},
		{"BDU off in a power-down", 0x20, 0},
		{"SIM (3-wire SPI) set", 0x20, pd | bdu | 1<<0},
		{"RESET_AZ set", 0x20, pd | bdu | 1<<1},
		{"DIFF_EN set: pressure would be differential", 0x20, pd | bdu | 1<<3},
		{"a reserved ODR", 0x20, pd | bdu | 5<<4},
		{"AUTOZERO set", 0x21, 1 << 1},
		{"SWRESET set", 0x21, 1 << 2},
		{"FIFO_EN set", 0x21, 1 << 6},
		{"BOOT set", 0x21, 1 << 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newLPS(t)
			before := l.m.Peek(tc.reg)
			err := l.write(tc.reg, tc.val)
			if !errors.Is(err, vanetest.ErrProtocol) || len(l.rec.msgs) != 1 {
				t.Fatalf("error = %v, reported %v", err, l.rec.msgs)
			}
			if l.m.Peek(tc.reg) != before {
				t.Error("the refused write took effect")
			}
		})
	}
	l := newLPS(t)
	if err := l.write(0x20, bdu); err != nil {
		t.Errorf("power-down with BDU refused: %v", err)
	}
}

func TestLPS25HWritableSet(t *testing.T) {
	l := newLPS(t)
	if err := l.write(0x20, bdu); err != nil {
		t.Errorf("CTRL_REG1: %v", err)
	}
	if err := l.write(0x21, 0); err != nil {
		t.Errorf("CTRL_REG2: %v", err)
	}
	// Writable on the chip, but vane has no reason to write them.
	for _, reg := range []uint8{0x08, 0x10, 0x22, 0x23, 0x24, 0x2E, 0x30, 0x39, 0x0F, 0x27, 0x28} {
		if err := l.write(reg, 0); !errors.Is(err, vanetest.ErrProtocol) {
			t.Errorf("write to 0x%02x: error = %v, want a protocol violation", reg, err)
		}
	}
}

func TestLPS25HReportsOverlappingOneShotSequences(t *testing.T) {
	t.Run("a trigger during a conversion", func(t *testing.T) {
		l := newLPS(t, vanetest.WithConversionPolls(3))
		_ = l.write(0x20, oneShotL)
		_ = l.write(0x21, oneShot)
		if err := l.write(0x21, oneShot); !errors.Is(err, vanetest.ErrProtocol) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("a trigger before the sample was read", func(t *testing.T) {
		l := newLPS(t)
		l.convert()
		if err := l.write(0x21, oneShot); !errors.Is(err, vanetest.ErrProtocol) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("a trigger after the sample was read is fine", func(t *testing.T) {
		l := newLPS(t)
		_ = l.write(0x20, oneShotL)
		for range 3 {
			if err := l.write(0x21, oneShot); err != nil {
				t.Fatal(err)
			}
			_ = l.status()
			l.outputs()
		}
		if len(l.rec.msgs) != 0 {
			t.Fatalf("reported %v", l.rec.msgs)
		}
	})
}

func TestLPS25HResetLosesTheConfiguration(t *testing.T) {
	l := newLPS(t)
	_ = l.write(0x20, oneShotL)
	l.m.Reset()
	if l.m.Peek(0x20) != 0 {
		t.Errorf("CTRL_REG1 after a reset = %#x", l.m.Peek(0x20))
	}
	_ = l.write(0x21, oneShot)
	for range 20 {
		if l.status() != 0 {
			t.Fatal("a reset chip completed a conversion")
		}
	}
}

func TestLPS25HStallAndRelease(t *testing.T) {
	l := newLPS(t)
	l.m.StallReady(true)
	_ = l.write(0x20, oneShotL)
	_ = l.write(0x21, oneShot)
	for range 20 {
		if l.status() != 0 {
			t.Fatal("a stalled chip completed a conversion")
		}
	}
	l.m.StallReady(false)
	if s := l.status(); s != pDA|tDA {
		t.Fatalf("status after release = %#x", s)
	}
	// And in continuous mode.
	c := newLPS(t)
	_ = c.write(0x20, pd|bdu|odr25Hz)
	c.m.StallReady(true)
	c.m.NewSample()
	if s := c.status(); s != 0 {
		t.Fatalf("a stalled chip produced a continuous sample: %#x", s)
	}
}

func TestLPS25HPowerDownStopsAConversion(t *testing.T) {
	l := newLPS(t, vanetest.WithConversionPolls(3))
	_ = l.write(0x20, oneShotL)
	_ = l.write(0x21, oneShot)
	_ = l.write(0x20, bdu)
	for range 10 {
		if l.status() != 0 {
			t.Fatal("a conversion finished after power-down")
		}
	}
}

func TestLPS25HStaggeredReadyRaisesTemperatureFirst(t *testing.T) {
	l := newLPS(t, vanetest.WithStaggeredReady())
	l.m.Set(1000, 42.5)
	_ = l.write(0x20, oneShotL)
	_ = l.write(0x21, oneShot)
	if s := l.status(); s != tDA {
		t.Fatalf("first status = %#x, want only T_DA", s)
	}
	if s := l.status(); s != pDA|tDA {
		t.Fatalf("second status = %#x, want both", s)
	}
	if p, _ := l.outputs(); p != 4096000 {
		t.Errorf("pressure counts = %d", p)
	}
}
