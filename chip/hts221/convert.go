package hts221

// The chip's operating range for temperature, from the datasheet (to confirm,
// see docs/hardware.md). A reading outside it is kept as converted and flagged.
const (
	tempMinOperating = -40.0
	tempMaxOperating = 120.0
)

// humidity converts a raw humidity count to %RH by linear interpolation
// between the two calibration points, and clamps the result to the physical
// range 0 to 100. clamped reports that the clamp changed it. The calibration
// must have passed Validate, which is what keeps the divisor non-zero.
func (c Calibration) humidity(raw int16) (rh float64, clamped bool) {
	rh = interpolate(raw, c.H0T0Out, c.H1T0Out, c.H0RH, c.H1RH)
	switch {
	case rh < 0:
		return 0, true
	case rh > 100:
		return 100, true
	}
	return rh, false
}

// temperature converts a raw temperature count to degrees Celsius.
func (c Calibration) temperature(raw int16) float64 {
	return interpolate(raw, c.T0Out, c.T1Out, c.T0DegC, c.T1DegC)
}

// interpolate returns the value on the straight line through (x0, y0) and
// (x1, y1) at x. The counts are widened first: the difference of two int16
// does not fit in an int16.
func interpolate(x, x0, x1 int16, y0, y1 float64) float64 {
	return y0 + float64(int(x)-int(x0))*(y1-y0)/float64(int(x1)-int(x0))
}
