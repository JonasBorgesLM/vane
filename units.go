package vane

// Celsius is a temperature in degrees Celsius. Units are named types so the
// compiler rejects mixing them: a Hectopascal cannot be passed where a Celsius
// is expected.
type Celsius float64

// RelativeHumidity is relative humidity in percent, 0–100 once clamped.
// A driver clamps an out-of-range conversion and sets FlagClamped.
type RelativeHumidity float64

// Hectopascal is a pressure in hectopascals (millibar).
type Hectopascal float64

// StandardGravity is a linear acceleration in multiples of standard gravity,
// g = 9.80665 m/s². It is the unit the LSM9DS1's full scales and sensitivities
// are given in (ADR-0008); a consumer wanting m/s² multiplies by g.
type StandardGravity float64

// DegreesPerSecond is an angular rate in degrees per second (dps), the unit of
// the LSM9DS1 gyroscope's full scales.
type DegreesPerSecond float64

// Gauss is a magnetic flux density in gauss (1 G = 100 µT), the unit of the
// LSM9DS1 magnetometer's full scales.
type Gauss float64

// Counts is a raw ADC count, dimensionless: what a sensor such as the TCS3400
// produced, before any conversion. Its meaning depends on the gain and
// integration time it was taken with, which travel with it (ADR-0009).
type Counts float64
