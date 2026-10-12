package lps25h

// Operating ranges used to flag a reading, from memory except the 1260 hPa
// full scale (kernel st_pressure_core.c:386); see docs/hardware.md. A reading
// outside them is kept as converted and flagged.
//
// There is no lower temperature bound on purpose. A 16-bit count at 480 per
// degree around 42.5 degC cannot represent anything below -25.77 degC, so a
// bound at the datasheet's -30 degC could never fire.
const (
	pressureMinOperating = 260.0
	pressureMaxOperating = 1260.0
	tempMaxOperating     = 105.0
)

// pressure converts a raw 24-bit count to hectopascals.
func pressure(raw int32) float64 { return float64(raw) / 4096 }

// temperature converts a raw 16-bit count to degrees Celsius.
func temperature(raw int16) float64 { return float64(raw)/480 + 42.5 }

// int24LE reads a signed 24-bit little-endian value, the pressure output format.
// Skipping the sign extension still gives plausible numbers at room conditions,
// where the top bit is clear, and goes wrong exactly when the output is a
// differential reading below zero.
func int24LE(b0, b1, b2 byte) int32 {
	v := int32(b0) | int32(b1)<<8 | int32(b2)<<16
	if v&0x800000 != 0 {
		v -= 1 << 24
	}
	return v
}

// int16LE reads a signed 16-bit little-endian value, the temperature format.
func int16LE(lo, hi byte) int16 {
	return int16(uint16(hi)<<8 | uint16(lo)) // #nosec G115 -- reinterpreting two's complement is the point
}
