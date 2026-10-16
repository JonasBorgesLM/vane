package vane

import (
	"strings"
	"time"
)

// Chip names a sensor chip. LPS25H stands for the register-compatible family
// LPS25H and LPS25HB, which cannot be told apart by their identity register
// (ADR-0003). LSM9DS1 is one chip at two addresses; Source.Address tells its
// accelerometer and gyroscope (0x6A) from its magnetometer (0x1C).
type Chip string

// The chips vane drives.
const (
	ChipHTS221  Chip = "HTS221"
	ChipLPS25H  Chip = "LPS25H"
	ChipLSM9DS1 Chip = "LSM9DS1"
)

// Source identifies where a measurement came from.
type Source struct {
	Chip    Chip
	Bus     string // e.g. "/dev/i2c-1", or a fake's name
	Address uint16
}

// Flag describes the quality of a measurement. A flag is information, never an
// error: a flagged value is still returned (RF-12).
type Flag uint8

// Quality flags. They combine with bitwise or.
const (
	// FlagClamped: the converted value was outside the physical range and was
	// clamped to it (for example relative humidity above 100 %).
	FlagClamped Flag = 1 << iota
	// FlagOutOfRange: the value is outside the datasheet's operating range. It
	// is kept as converted.
	FlagOutOfRange
	// FlagStale: continuous mode, no new sample since the previous read.
	FlagStale
	// FlagOverrun: continuous mode, the chip overwrote a sample nobody read.
	FlagOverrun
)

var flagNames = [...]string{"clamped", "out-of-range", "stale", "overrun"}

// Has reports whether every flag in x is set in f.
func (f Flag) Has(x Flag) bool { return f&x == x }

// String lists the set flags separated by "|", in declaration order, or ""
// when none is set.
func (f Flag) String() string {
	var names []string
	for i, n := range flagNames {
		if f&(1<<i) != 0 {
			names = append(names, n)
		}
	}
	return strings.Join(names, "|")
}

// Measurement is one reading with everything needed to audit it. It is a value
// object: drivers return it filled and nothing mutates it afterwards.
type Measurement[T ~float64] struct {
	// Value is the converted reading in the unit T.
	Value T
	// Raw is the ADC count before conversion, sign-extended. Keeping it makes
	// the conversion auditable and recalibration possible (G-04).
	Raw int32
	// Source says which chip, on which bus and address, produced the reading.
	Source Source
	// Time is taken after data-ready, from the driver's injected clock.
	Time time.Time
	// Flags carries the quality of the reading; see Flag.
	Flags Flag
}
