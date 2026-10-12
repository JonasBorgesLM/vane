package hts221

import (
	"fmt"

	"github.com/JonasBorgesLM/vane"
)

const (
	calibrationStart = 0x30
	calibrationLen   = 16 // registers 0x30 to 0x3F
)

// Calibration holds the chip's factory calibration: two reference points for
// humidity and two for temperature, each with the raw count the chip reads at
// it. Conversion is linear interpolation between them.
type Calibration struct {
	H0RH, H1RH     float64 // relative humidity at the two points, %RH
	T0DegC, T1DegC float64 // temperature at the two points, °C
	H0T0Out        int16   // raw humidity count at H0RH
	H1T0Out        int16   // raw humidity count at H1RH
	T0Out          int16   // raw temperature count at T0DegC
	T1Out          int16   // raw temperature count at T1DegC
}

// parseCalibration decodes registers 0x30 to 0x3F. The temperature points are
// ten bits each: eight in registers 0x32 and 0x33, the top two in register
// 0x35, bits 1:0 for T0 and bits 3:2 for T1, in units of an eighth of a degree.
// Registers 0x34, 0x38 and 0x39 are reserved and ignored.
func parseCalibration(b []byte) Calibration {
	t0 := int(b[0x35-calibrationStart]&0x03)<<8 | int(b[0x32-calibrationStart])
	t1 := int(b[0x35-calibrationStart]>>2&0x03)<<8 | int(b[0x33-calibrationStart])
	return Calibration{
		H0RH:    float64(b[0x30-calibrationStart]) / 2,
		H1RH:    float64(b[0x31-calibrationStart]) / 2,
		T0DegC:  float64(t0) / 8,
		T1DegC:  float64(t1) / 8,
		H0T0Out: int16LE(b[0x36-calibrationStart], b[0x37-calibrationStart]),
		H1T0Out: int16LE(b[0x3A-calibrationStart], b[0x3B-calibrationStart]),
		T0Out:   int16LE(b[0x3C-calibrationStart], b[0x3D-calibrationStart]),
		T1Out:   int16LE(b[0x3E-calibrationStart], b[0x3F-calibrationStart]),
	}
}

// int16LE reads a signed 16-bit little-endian value, the chip's output format.
func int16LE(lo, hi byte) int16 {
	return int16(uint16(hi)<<8 | uint16(lo)) // #nosec G115 -- reinterpreting two's complement is the point
}

// Validate reports whether the calibration can be used to convert a reading.
// Conversion divides by the distance between the two points, so two equal
// counts, or two equal reference values that would make the line flat, cannot
// be used. Identical points have been reported on real chips, apparently from
// read errors, and the reference Linux driver does not check for them. The error
// matches vane.ErrCalibration.
func (c Calibration) Validate() error {
	switch {
	case c.H0T0Out == c.H1T0Out:
		return fmt.Errorf("%w: both humidity points read the same count, %d", vane.ErrCalibration, c.H0T0Out)
	case c.T0Out == c.T1Out:
		return fmt.Errorf("%w: both temperature points read the same count, %d", vane.ErrCalibration, c.T0Out)
	case c.H0RH == c.H1RH:
		return fmt.Errorf("%w: both humidity points are at %g %%RH", vane.ErrCalibration, c.H0RH)
	case c.T0DegC == c.T1DegC:
		return fmt.Errorf("%w: both temperature points are at %g °C", vane.ErrCalibration, c.T0DegC)
	}
	return nil
}
