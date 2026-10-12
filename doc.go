// Package vane holds the types shared by the Sense HAT drivers: units,
// measurements, their source, errors and the Bus seam.
//
// A Measurement carries the converted value in a named unit (Celsius,
// RelativeHumidity, Hectopascal), the raw ADC count it came from, which chip
// produced it, when, and quality Flags. vane never compensates for the heat of
// the Pi's CPU; it reports each chip's own temperature so the consumer can.
//
// The package is a leaf: it imports no other package of this module, so the
// chip drivers can depend on it and the board facade can depend on the drivers
// without a cycle. The drivers live in chip/hts221 and chip/lps25h, the board
// facade in sensehat, and the Linux I2C backend in i2cdev.
package vane
