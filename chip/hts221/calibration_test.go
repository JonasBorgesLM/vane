package hts221

import (
	"errors"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane"
)

// block builds registers 0x30 to 0x3F with the given bytes at the registers
// that matter; the reserved ones hold a marker that must never be read as data.
func block(h0, h1, t0lo, t1lo, msb byte, h0out, h1out, t0out, t1out int16) []byte {
	b := make([]byte, calibrationLen)
	for i := range b {
		b[i] = 0xEE
	}
	b[0x30-calibrationStart], b[0x31-calibrationStart] = h0, h1
	b[0x32-calibrationStart], b[0x33-calibrationStart] = t0lo, t1lo
	b[0x35-calibrationStart] = msb
	put := func(reg int, v int16) {
		b[reg-calibrationStart] = byte(uint16(v))
		b[reg+1-calibrationStart] = byte(uint16(v) >> 8)
	}
	put(0x36, h0out)
	put(0x3A, h1out)
	put(0x3C, t0out)
	put(0x3E, t1out)
	return b
}

// Each case decoded by hand from the register layout in docs/hardware.md.
func TestParseCalibration(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want Calibration
	}{
		{
			name: "the model's factory block: 20 and 80 %RH, 20 and 80 degC",
			// T1 = 640/8: low byte 0x80, high bits 2 in bits 3:2 of 0x35 (0b1000).
			b:    block(40, 160, 160, 0x80, 0x08, -500, 7500, -200, 2200),
			want: Calibration{H0RH: 20, H1RH: 80, T0DegC: 20, T1DegC: 80, H0T0Out: -500, H1T0Out: 7500, T0Out: -200, T1Out: 2200},
		},
		{
			name: "the ten-bit maxima: 1023/8 degC for both",
			b:    block(0, 200, 0xFF, 0xFF, 0x0F, 1, 2, 3, 4),
			want: Calibration{H0RH: 0, H1RH: 100, T0DegC: 127.875, T1DegC: 127.875, H0T0Out: 1, H1T0Out: 2, T0Out: 3, T1Out: 4},
		},
		{
			name: "the high bits belong to their own point: T0 gets bits 1:0, T1 gets bits 3:2",
			b:    block(0, 0, 0x00, 0x00, 0b0110, 0, 1, 0, 1), // T0 high = 2 (512/8 = 64), T1 high = 1 (256/8 = 32)
			want: Calibration{T0DegC: 64, T1DegC: 32, H1T0Out: 1, T1Out: 1},
		},
		{
			name: "bits 7:4 of the high-bits register are not temperature",
			b:    block(0, 0, 0x08, 0x10, 0xF0, 0, 1, 0, 1), // only the reserved upper nibble is set
			want: Calibration{T0DegC: 1, T1DegC: 2, H1T0Out: 1, T1Out: 1},
		},
		{
			name: "counts are signed little-endian",
			b:    block(0, 0, 0, 0, 0, -32768, 32767, -1, 1),
			want: Calibration{H0T0Out: -32768, H1T0Out: 32767, T0Out: -1, T1Out: 1},
		},
		{
			name: "half-percent resolution",
			b:    block(41, 161, 0, 0, 0, 0, 1, 0, 1),
			want: Calibration{H0RH: 20.5, H1RH: 80.5, H1T0Out: 1, T1Out: 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCalibration(tc.b); got != tc.want {
				t.Errorf("parseCalibration =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

func TestParseIgnoresTheReservedRegisters(t *testing.T) {
	a := block(40, 160, 160, 0x80, 0x08, -500, 7500, -200, 2200)
	b := append([]byte(nil), a...)
	for _, reg := range []int{0x34, 0x38, 0x39} {
		b[reg-calibrationStart] ^= 0xFF
	}
	if parseCalibration(a) != parseCalibration(b) {
		t.Fatal("a reserved register changed the calibration")
	}
}

// Negative control, seen failing: a Validate that does not look at one of the
// four conditions lets that case through.
func TestValidate(t *testing.T) {
	good := Calibration{H0RH: 20, H1RH: 80, T0DegC: 20, T1DegC: 80, H0T0Out: -500, H1T0Out: 7500, T0Out: -200, T1Out: 2200}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good calibration was refused: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Calibration)
		want   string
	}{
		{"humidity counts equal (the case reported on real chips)", func(c *Calibration) { c.H1T0Out = c.H0T0Out }, "humidity points read the same count"},
		{"humidity counts both zero", func(c *Calibration) { c.H0T0Out, c.H1T0Out = 0, 0 }, "same count, 0"},
		{"temperature counts equal", func(c *Calibration) { c.T1Out = c.T0Out }, "temperature points read the same count"},
		{"humidity references equal", func(c *Calibration) { c.H1RH = c.H0RH }, "humidity points are at 20"},
		{"temperature references equal", func(c *Calibration) { c.T1DegC = c.T0DegC }, "temperature points are at 20"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mutate(&c)
			err := c.Validate()
			if !errors.Is(err, vane.ErrCalibration) || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want ErrCalibration mentioning %q", err, tc.want)
			}
		})
	}
}

// isDegenerate decides the same question as Validate from the raw register
// bytes, without parsing, so the fuzz target compares two separate derivations.
func isDegenerate(b []byte) bool {
	same := func(lo1, hi1, lo2, hi2 int) bool {
		return b[lo1-calibrationStart] == b[lo2-calibrationStart] && b[hi1-calibrationStart] == b[hi2-calibrationStart]
	}
	t0 := int(b[0x35-calibrationStart]&0x03)<<8 | int(b[0x32-calibrationStart])
	t1 := int(b[0x35-calibrationStart]>>2&0x03)<<8 | int(b[0x33-calibrationStart])
	return same(0x36, 0x37, 0x3A, 0x3B) || same(0x3C, 0x3D, 0x3E, 0x3F) ||
		b[0x30-calibrationStart] == b[0x31-calibrationStart] || t0 == t1
}

// FuzzCalibration checks that no register image panics the parser, that
// Validate accepts exactly the images that are not degenerate, and that the
// error is always ErrCalibration. The seed corpus holds the case reported on
// real chips, in which both humidity points read the same count.
func FuzzCalibration(f *testing.F) {
	f.Add(block(40, 160, 160, 0x80, 0x08, -500, 7500, -200, 2200))
	f.Add(block(40, 160, 160, 0x80, 0x08, 0, 0, -200, 2200))        // identical humidity counts, the reported case
	f.Add(block(40, 160, 160, 0x80, 0x08, -500, 7500, 0, 0))        // identical temperature counts
	f.Add(block(40, 40, 160, 0x80, 0x08, -500, 7500, -200, 2200))   // identical humidity references
	f.Add(block(40, 160, 0x80, 0x80, 0x00, -500, 7500, -200, 2200)) // identical temperature references
	f.Add(block(0, 0, 0, 0, 0, 0, 0, 0, 0))
	f.Add(block(0xFF, 0xFF, 0xFF, 0xFF, 0xFF, -1, -1, -1, -1))
	f.Add(make([]byte, calibrationLen))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) != calibrationLen {
			t.Skip()
		}
		cal := parseCalibration(b)
		err := cal.Validate()
		switch degenerate := isDegenerate(b); {
		case degenerate && err == nil:
			t.Fatalf("a degenerate calibration was accepted: %x -> %+v", b, cal)
		case !degenerate && err != nil:
			t.Fatalf("a usable calibration was refused: %x -> %+v: %v", b, cal, err)
		case err != nil && !errors.Is(err, vane.ErrCalibration):
			t.Fatalf("error %v does not match ErrCalibration", err)
		}
	})
}
