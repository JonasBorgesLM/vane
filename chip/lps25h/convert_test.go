package lps25h

import (
	"math"
	"testing"
)

// Vectors computed by hand from the datasheet's two formulas, hPa = count / 4096
// and degC = count / 480 + 42.5. Neither the datasheet nor ST's reference code
// publishes worked numbers, so each is derived here and exact in float64.
func TestPressureVectors(t *testing.T) {
	for _, tc := range []struct {
		raw  int32
		want float64
	}{
		{0, 0},
		{4096, 1},
		{4150272, 1013.25}, // standard atmosphere: 1013.25 * 4096
		{5160960, 1260},    // the full scale
		{1064960, 260},     // the lower end of the operating range
		{-40960, -10},      // a differential reading below zero
		{8388607, 2047.999755859375},
		{-8388608, -2048},
	} {
		if got := pressure(tc.raw); got != tc.want {
			t.Errorf("pressure(%d) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestTemperatureVectors(t *testing.T) {
	for _, tc := range []struct {
		raw  int16
		want float64
	}{
		{0, 42.5},     // the zero of the scale
		{480, 43.5},   // one degree per 480 counts
		{-9600, 22.5}, // a room: 20 degrees below the zero
		{4800, 52.5},
		{30000, 105}, // 62.5 degrees above the zero
		{-30000, -20},
	} {
		if got := temperature(tc.raw); got != tc.want {
			t.Errorf("temperature(%d) = %v, want %v", tc.raw, got, tc.want)
		}
	}
	// The ends of the 16-bit range, which the formula puts at -25.77 and 110.77
	// degC: nothing below -25.77 can be reported, whatever the datasheet's
	// stated operating range.
	if got := temperature(math.MinInt16); math.Abs(got-(-25.766666666666666)) > 1e-12 {
		t.Errorf("temperature(min) = %v", got)
	}
	if got := temperature(math.MaxInt16); math.Abs(got-110.76458333333333) > 1e-12 {
		t.Errorf("temperature(max) = %v", got)
	}
}

// Negative control, seen failing: reading the 24 bits as unsigned turns every
// negative reading into a pressure near 4096 hPa, and room conditions look fine.
func TestInt24IsSignExtended(t *testing.T) {
	for _, tc := range []struct {
		b    [3]byte
		want int32
	}{
		{[3]byte{0x00, 0x54, 0x3F}, 4150272},
		{[3]byte{0x00, 0x60, 0xFF}, -40960},
		{[3]byte{0xFF, 0xFF, 0xFF}, -1},
		{[3]byte{0x00, 0x00, 0x80}, -8388608},
		{[3]byte{0xFF, 0xFF, 0x7F}, 8388607},
		{[3]byte{0x00, 0x00, 0x00}, 0},
		{[3]byte{0x01, 0x00, 0x00}, 1},
		{[3]byte{0x00, 0x00, 0x01}, 65536},
	} {
		if got := int24LE(tc.b[0], tc.b[1], tc.b[2]); got != tc.want {
			t.Errorf("int24LE(% x) = %d, want %d", tc.b, got, tc.want)
		}
	}
}

func TestInt16IsSignedLittleEndian(t *testing.T) {
	for _, tc := range []struct {
		lo, hi byte
		want   int16
	}{{0x80, 0xDA, -9600}, {0x00, 0x80, -32768}, {0xFF, 0x7F, 32767}, {0xFF, 0xFF, -1}, {0x01, 0x00, 1}} {
		if got := int16LE(tc.lo, tc.hi); got != tc.want {
			t.Errorf("int16LE(%#x, %#x) = %d, want %d", tc.lo, tc.hi, got, tc.want)
		}
	}
}

// FuzzConvert checks, for every five output bytes, that the conversion is
// finite, that sign extension agrees with an independent two's complement
// derivation, and that one more count never lowers the value.
func FuzzConvert(f *testing.F) {
	f.Add(byte(0x00), byte(0x54), byte(0x3F), byte(0x80), byte(0xDA))
	f.Add(byte(0x00), byte(0x60), byte(0xFF), byte(0x00), byte(0x80))
	f.Add(byte(0xFF), byte(0xFF), byte(0x7F), byte(0xFF), byte(0x7F))
	f.Add(byte(0), byte(0), byte(0), byte(0), byte(0))
	f.Add(byte(0xFF), byte(0xFF), byte(0xFF), byte(0xFF), byte(0xFF))
	f.Fuzz(func(t *testing.T, p0, p1, p2, t0, t1 byte) {
		raw := int24LE(p0, p1, p2)
		if raw < -(1<<23) || raw > 1<<23-1 {
			t.Fatalf("int24LE(%#x %#x %#x) = %d is outside 24 bits", p0, p1, p2, raw)
		}
		// Independent derivation: the unsigned value minus 2^24 when the top bit is set.
		u := uint32(p0) | uint32(p1)<<8 | uint32(p2)<<16
		want := int64(u)
		if u >= 1<<23 {
			want -= 1 << 24
		}
		if int64(raw) != want {
			t.Fatalf("int24LE = %d, two's complement of %#x is %d", raw, u, want)
		}
		rawT := int16LE(t0, t1)
		for name, v := range map[string]float64{"pressure": pressure(raw), "temperature": temperature(rawT)} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("%s = %v", name, v)
			}
		}
		if raw < 1<<23-1 && pressure(raw+1) < pressure(raw) {
			t.Fatalf("pressure is not monotonic at %d", raw)
		}
		if rawT < math.MaxInt16 && temperature(rawT+1) < temperature(rawT) {
			t.Fatalf("temperature is not monotonic at %d", rawT)
		}
	})
}
