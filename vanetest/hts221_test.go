package vanetest_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

const (
	pd       = 1 << 7
	bdu      = 1 << 2
	oneShot  = 1 << 0
	hDA, tDA = 1 << 1, 1 << 0
)

type hts struct {
	t   *testing.T
	m   *vanetest.HTS221Model
	rec *recorder
}

func newHTS(t *testing.T, opts ...vanetest.ModelOption) *hts {
	rec := &recorder{}
	m := vanetest.NewHTS221(append([]vanetest.ModelOption{vanetest.WithReporter(rec)}, opts...)...)
	return &hts{t: t, m: m, rec: rec}
}

func (h *hts) write(reg uint8, vals ...byte) error {
	h.t.Helper()
	return h.m.Tx(append([]byte{reg}, vals...), nil)
}

func (h *hts) read(reg uint8, n int) []byte {
	h.t.Helper()
	w := reg
	if n > 1 {
		w |= 0x80
	}
	r := make([]byte, n)
	if err := h.m.Tx([]byte{w}, r); err != nil {
		h.t.Fatalf("read 0x%02x x%d: %v", reg, n, err)
	}
	return r
}

func (h *hts) status() byte { return h.read(0x27, 1)[0] }

// outputs reads humidity and temperature as the chip hands them out: two
// signed little-endian words.
func (h *hts) outputs() (hum, temp int16) {
	b := h.read(0x28, 4)
	// #nosec G115 -- the chip's outputs are two's complement; reinterpreting them is the point
	return int16(uint16(b[0]) | uint16(b[1])<<8), int16(uint16(b[2]) | uint16(b[3])<<8)
}

func TestHTS221IdentityAndPowerOnState(t *testing.T) {
	h := newHTS(t)
	if got := h.read(0x0F, 1)[0]; got != 0xBC {
		t.Errorf("WHO_AM_I = %#x, want 0xbc", got)
	}
	for reg, want := range map[uint8]byte{0x10: 0x1B, 0x20: 0, 0x21: 0, 0x22: 0, 0x27: 0} {
		if got := h.m.Peek(reg); got != want {
			t.Errorf("register 0x%02x at power-on = %#x, want %#x", reg, got, want)
		}
	}
	if h.m.Addr() != 0x5F {
		t.Errorf("address = %#x", h.m.Addr())
	}
	if got := newHTS(t, vanetest.WithWhoAmI(0xBD)).read(0x0F, 1)[0]; got != 0xBD {
		t.Errorf("WithWhoAmI: %#x", got)
	}
}

// The block as the datasheet lays it out, decoded by hand: 20 %RH is 40 in
// units of half a percent; 20 °C is 160 and 80 °C is 640 in units of an eighth
// of a degree, the latter needing the two high bits kept in register 0x35.
func TestHTS221CalibrationBlock(t *testing.T) {
	h := newHTS(t)
	b := h.read(0x30, 16)
	want := map[int]byte{
		0:  40,   // 0x30 H0_rH_x2 = 20 %RH
		1:  160,  // 0x31 H1_rH_x2 = 80 %RH
		2:  160,  // 0x32 T0_degC_x8 low = 20 °C
		3:  0x80, // 0x33 T1_degC_x8 low = 640 & 0xff
		5:  0x08, // 0x35 T1 high bits (640 >> 8 = 2) in bits 3:2, T0 high bits 0
		6:  0x0C, // 0x36 H0_T0_OUT = -500 = 0xFE0C
		7:  0xFE,
		10: 0x4C, // 0x3A H1_T0_OUT = 7500 = 0x1D4C
		11: 0x1D,
		12: 0x38, // 0x3C T0_OUT = -200 = 0xFF38
		13: 0xFF,
		14: 0x98, // 0x3E T1_OUT = 2200 = 0x0898
		15: 0x08,
	}
	for i, v := range want {
		if b[i] != v {
			t.Errorf("calibration byte 0x%02x = %#02x, want %#02x", 0x30+i, b[i], v)
		}
	}
	f := h.m.Factory()
	if f.H0RH != 20 || f.H1RH != 80 || f.T0DegC != 20 || f.T1DegC != 80 || f.H0T0Out != -500 || f.T1Out != 2200 {
		t.Errorf("Factory() = %+v", f)
	}
}

func TestHTS221SetCountsLieOnTheCalibrationLine(t *testing.T) {
	// 50 %RH is 30 above H0 on a line of 8000 counts per 60 %: -500 + 4000.
	// 50 °C is 30 above T0 on 2400 counts per 60 °C: -200 + 1200.
	for _, tc := range []struct {
		rh, t        float64
		wantH, wantT int16
	}{
		{20, 20, -500, -200},
		{80, 80, 7500, 2200},
		{50, 50, 3500, 1000},
		{0, 0, -500 - 2667, -200 - 800}, // 20 below H0: 20*8000/60 = 2666.67, rounds to 2667
		{400, -1000, 32767, -32768},     // far outside the line: 380 %RH is 50167 counts, 1020 °C below T0 is -41000; both saturate, neither wraps
	} {
		h := newHTS(t)
		h.m.Set(vanetest.HTS221Humidity(tc.rh), vanetest.HTS221Temperature(tc.t))
		_ = h.write(0x20, pd|bdu)
		_ = h.write(0x21, oneShot)
		_ = h.status()
		gotH, gotT := h.outputs()
		if gotH != tc.wantH || gotT != tc.wantT {
			t.Errorf("Set(%v, %v): counts %d, %d, want %d, %d", tc.rh, tc.t, gotH, gotT, tc.wantH, tc.wantT)
		}
	}
}

func TestHTS221OneShot(t *testing.T) {
	h := newHTS(t, vanetest.WithConversionPolls(2))
	h.m.Set(50, 50)
	if err := h.write(0x20, pd|bdu); err != nil {
		t.Fatal(err)
	}
	if err := h.write(0x21, oneShot); err != nil {
		t.Fatal(err)
	}
	if got := h.m.Peek(0x21); got&oneShot == 0 {
		t.Fatalf("ONE_SHOT cleared before the conversion ended: %#x", got)
	}
	for i := range 2 {
		if s := h.status(); s != 0 {
			t.Fatalf("status read %d = %#x, want not ready", i, s)
		}
	}
	if s := h.status(); s != hDA|tDA {
		t.Fatalf("status after the conversion = %#x, want both data-ready bits", s)
	}
	if got := h.m.Peek(0x21); got&oneShot != 0 {
		t.Errorf("ONE_SHOT still set after the conversion: %#x", got)
	}
	if hum, temp := h.outputs(); hum != 3500 || temp != 1000 {
		t.Errorf("outputs = %d, %d", hum, temp)
	}
	if s := h.status(); s != 0 {
		t.Errorf("status after both outputs were read = %#x, want the flags cleared", s)
	}
}

func TestHTS221OneShotIsIgnoredInContinuousMode(t *testing.T) {
	h := newHTS(t)
	_ = h.write(0x20, pd|bdu|1)
	_ = h.write(0x21, oneShot)
	for range 50 {
		if s := h.status(); s != 0 {
			t.Fatalf("a one-shot request completed (status %#x)", s)
		}
	}
	if h.m.Peek(0x21)&oneShot != 0 {
		t.Errorf("an ignored request left ONE_SHOT set")
	}
}

// Observed on a Pi (docs/hardware.md): the request is latched, not dropped.
func TestHTS221OneShotWhilePoweredDownRunsOnPowerUp(t *testing.T) {
	h := newHTS(t)
	_ = h.write(0x20, bdu)
	_ = h.write(0x21, oneShot)
	for range 50 {
		if s := h.status(); s != 0 {
			t.Fatalf("a conversion ran while powered down (status %#x)", s)
		}
	}
	if h.m.Peek(0x21)&oneShot == 0 {
		t.Fatalf("ONE_SHOT written while powered down was dropped, want it latched")
	}
	_ = h.write(0x20, pd|bdu)
	for i := 0; h.status() != hDA|tDA; i++ {
		if i == 50 {
			t.Fatalf("the latched one-shot did not run on power-up")
		}
	}
	if h.m.Peek(0x21)&oneShot != 0 {
		t.Errorf("ONE_SHOT still set after the latched conversion")
	}
}

// The high byte of an output clears its flag; the low byte does not
// (DocID026333 Rev 4 p. 24; on the board, HUMIDITY_OUT_H alone took STATUS_REG
// from 0x03 to 0x01). Negative control: the earlier model, which waited for both
// bytes, fails the high-byte-first half.
func TestHTS221AFlagClearsWhenTheHighByteOfItsOutputIsRead(t *testing.T) {
	h := newHTS(t)
	_ = h.write(0x20, pd|bdu)
	_ = h.write(0x21, oneShot)
	_ = h.status()
	h.read(0x28, 1)
	if s := h.status(); s != hDA|tDA {
		t.Fatalf("after HUMIDITY_OUT_L: status %#x, want both flags still set", s)
	}
	h.read(0x29, 1)
	if s := h.status(); s != tDA {
		t.Fatalf("after HUMIDITY_OUT_H: status %#x, want only T_DA left", s)
	}
	h.read(0x2B, 1) // TEMP_OUT_H alone, its low byte never read
	if s := h.status(); s != 0 {
		t.Fatalf("after TEMP_OUT_H alone: status %#x, want 0", s)
	}
}

// Under BDU a sample may not replace one that has not been read. Without BDU
// the register would change under the reader, which the model does not
// emulate; the rule it enforces instead is that vane never turns BDU off.
func TestHTS221BlockDataUpdateHoldsAnUnreadSample(t *testing.T) {
	h := newHTS(t)
	h.m.Set(30, 30)
	_ = h.write(0x20, pd|bdu|1) // continuous, 1 Hz
	h.m.NewSample()
	h.m.Set(70, 70)
	h.m.NewSample() // arrives while the first is unread: lost
	first, _ := h.outputs()
	h.m.NewSample() // arrives after the read: kept
	second, _ := h.outputs()
	// 30 %RH is 10 above H0: -500 + 1333.33 = 833. 70 %RH is 50 above: 6166.67 = 6167.
	if first != 833 {
		t.Errorf("first sample = %d, was replaced before it was read", first)
	}
	if second != 6167 {
		t.Errorf("second sample = %d", second)
	}
}

func TestHTS221NewSampleOnlyInContinuousMode(t *testing.T) {
	h := newHTS(t)
	h.m.NewSample() // power-down
	_ = h.write(0x20, pd|bdu)
	h.m.NewSample() // one-shot mode
	if s := h.status(); s != 0 {
		t.Fatalf("status = %#x, want no sample outside continuous mode", s)
	}
}

// Negative control, seen failing: removing the BDU check in checkWrite.
func TestHTS221RefusesToHaveBDUTurnedOff(t *testing.T) {
	for name, ctrl1 := range map[string]byte{"power-up without BDU": pd, "power-down without BDU": 0, "continuous without BDU": pd | 1} {
		h := newHTS(t)
		err := h.write(0x20, ctrl1)
		if !errors.Is(err, vanetest.ErrProtocol) || len(h.rec.msgs) != 1 {
			t.Errorf("%s: error = %v, reported %v", name, err, h.rec.msgs)
		}
		if h.m.Peek(0x20) != 0 {
			t.Errorf("%s: the refused write took effect", name)
		}
	}
	h := newHTS(t)
	if err := h.write(0x20, bdu); err != nil {
		t.Errorf("power-down with BDU refused: %v", err)
	}
	// A block write that reaches CTRL_REG1 from AV_CONF is checked too.
	h = newHTS(t)
	if err := h.write(0x10|0x80, 0x1B, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, pd); err == nil {
		t.Error("a block write that sets CTRL_REG1 without BDU was accepted")
	}
}

func TestHTS221WritableSet(t *testing.T) {
	h := newHTS(t)
	for _, reg := range []uint8{0x10, 0x20, 0x21, 0x22} {
		v := byte(0)
		if reg == 0x20 {
			v = bdu
		}
		if err := h.write(reg, v); err != nil {
			t.Errorf("write to 0x%02x refused: %v", reg, err)
		}
	}
	for _, reg := range []uint8{0x0F, 0x27, 0x28, 0x30, 0x3F} {
		if err := h.write(reg, 1); !errors.Is(err, vanetest.ErrProtocol) {
			t.Errorf("write to 0x%02x: error = %v, want a protocol violation", reg, err)
		}
	}
}

func TestHTS221ResetLosesTheConfigurationButNotTheCalibration(t *testing.T) {
	h := newHTS(t)
	_ = h.write(0x10, 0x00)
	_ = h.write(0x20, pd|bdu)
	cal := h.read(0x30, 16)
	h.m.Reset()
	if h.m.Peek(0x20) != 0 || h.m.Peek(0x10) != 0x1B {
		t.Errorf("after a reset CTRL_REG1 = %#x, AV_CONF = %#x", h.m.Peek(0x20), h.m.Peek(0x10))
	}
	if after := h.read(0x30, 16); !bytes.Equal(after, cal) {
		t.Errorf("calibration changed across a reset: %x -> %x", cal, after)
	}
	_ = h.write(0x21, oneShot) // powered down again: ignored
	for range 20 {
		if h.status() != 0 {
			t.Fatal("a reset chip completed a conversion")
		}
	}
}

func TestHTS221StallAndRelease(t *testing.T) {
	h := newHTS(t)
	h.m.StallReady(true)
	_ = h.write(0x20, pd|bdu)
	_ = h.write(0x21, oneShot)
	for range 20 {
		if h.status() != 0 {
			t.Fatal("a stalled chip completed a conversion")
		}
	}
	h.m.StallReady(false)
	if s := h.status(); s != hDA|tDA {
		t.Fatalf("status after release = %#x", s)
	}
}

func TestHTS221PowerDownStopsAConversion(t *testing.T) {
	h := newHTS(t, vanetest.WithConversionPolls(3))
	_ = h.write(0x20, pd|bdu)
	_ = h.write(0x21, oneShot)
	_ = h.write(0x20, bdu) // back to power-down
	for range 10 {
		if h.status() != 0 {
			t.Fatal("a conversion finished after power-down")
		}
	}
}

func TestHTS221StallAlsoStopsContinuousSamples(t *testing.T) {
	h := newHTS(t)
	_ = h.write(0x20, pd|bdu|1)
	h.m.StallReady(true)
	h.m.NewSample()
	if s := h.status(); s != 0 {
		t.Fatalf("a stalled chip produced a continuous sample: status %#x", s)
	}
	h.m.StallReady(false)
	h.m.NewSample()
	if s := h.status(); s != hDA|tDA {
		t.Fatalf("status after release = %#x", s)
	}
}

// Negative control, seen failing: without the check in checkWrite, a second
// trigger is accepted silently and the two sequences share one sample.
func TestHTS221ReportsOverlappingOneShotSequences(t *testing.T) {
	t.Run("a trigger during a conversion", func(t *testing.T) {
		h := newHTS(t, vanetest.WithConversionPolls(3))
		_ = h.write(0x20, pd|bdu)
		_ = h.write(0x21, oneShot)
		if err := h.write(0x21, oneShot); !errors.Is(err, vanetest.ErrProtocol) || len(h.rec.msgs) != 1 {
			t.Fatalf("error = %v, reported %v", err, h.rec.msgs)
		}
	})
	t.Run("a trigger before the sample was read", func(t *testing.T) {
		h := newHTS(t)
		_ = h.write(0x20, pd|bdu)
		_ = h.write(0x21, oneShot)
		_ = h.status() // the conversion completes; nobody reads the outputs
		if err := h.write(0x21, oneShot); !errors.Is(err, vanetest.ErrProtocol) {
			t.Fatalf("error = %v, want a protocol violation", err)
		}
	})
	t.Run("a trigger after the sample was read is fine", func(t *testing.T) {
		h := newHTS(t)
		_ = h.write(0x20, pd|bdu)
		for range 3 {
			if err := h.write(0x21, oneShot); err != nil {
				t.Fatal(err)
			}
			_ = h.status()
			h.outputs()
		}
		if len(h.rec.msgs) != 0 {
			t.Fatalf("a sequence run to the end reported %v", h.rec.msgs)
		}
	})
	t.Run("a trigger that the chip ignores is not a sequence", func(t *testing.T) {
		h := newHTS(t)
		_ = h.write(0x20, bdu) // power-down
		for range 2 {
			if err := h.write(0x21, oneShot); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestHTS221StaggeredReadyRaisesTemperatureFirst(t *testing.T) {
	h := newHTS(t, vanetest.WithStaggeredReady())
	h.m.Set(50, 50)
	_ = h.write(0x20, pd|bdu)
	_ = h.write(0x21, oneShot)
	if s := h.status(); s != tDA {
		t.Fatalf("first status = %#x, want only T_DA", s)
	}
	if s := h.status(); s != hDA|tDA {
		t.Fatalf("second status = %#x, want both", s)
	}
	if hum, temp := h.outputs(); hum != 3500 || temp != 1000 {
		t.Errorf("outputs = %d, %d", hum, temp)
	}
}
