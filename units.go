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
