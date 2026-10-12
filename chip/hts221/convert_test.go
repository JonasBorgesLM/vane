package hts221

import (
	"math"
	"math/rand"
	"testing"
)

// The calibration used by the hand-computed vectors: 20 %RH at -500 counts and
// 80 %RH at 7500, so 60 %RH spans 8000 counts (0.0075 %RH per count); 20 degC
// at -200 and 80 degC at 2200, so 60 degC spans 2400 counts (0.025 degC per
// count).
var vectorCal = Calibration{
	H0RH: 20, H1RH: 80, T0DegC: 20, T1DegC: 80,
	H0T0Out: -500, H1T0Out: 7500, T0Out: -200, T1Out: 2200,
}

// Vectors computed by hand from the two-point line and reviewed. The
// datasheet publishes no worked numbers for the HTS221, so there is no
// published vector to cite; each expected value is exact in float64.
func TestHumidityVectors(t *testing.T) {
	tests := []struct {
		raw     int16
		want    float64
		clamped bool
	}{
		{-500, 20, false}, // the first point
		{7500, 80, false}, // the second point
		{3500, 50, false}, // halfway: 20 + 4000 * 0.0075
		{0, 23.75, false}, // 20 + 500 * 0.0075
		{8000, 83.75, false},
		{9500, 95, false},  // past the second point, still on the line
		{11500, 100, true}, // 110 on the line
		{-4000, 0, true},   // -6.25 on the line
	}
	for _, tc := range tests {
		got, clamped := vectorCal.humidity(tc.raw)
		if got != tc.want || clamped != tc.clamped {
			t.Errorf("humidity(%d) = %v (clamped %v), want %v (clamped %v)", tc.raw, got, clamped, tc.want, tc.clamped)
		}
	}
}

func TestHumidityClampBoundaries(t *testing.T) {
	// 0 %RH at 0 counts and 100 %RH at 1000: every boundary lands on a count.
	c := Calibration{H0RH: 0, H1RH: 100, H0T0Out: 0, H1T0Out: 1000, T0DegC: 0, T1DegC: 1, T0Out: 0, T1Out: 1}
	for _, tc := range []struct {
		raw     int16
		want    float64
		clamped bool
	}{
		{0, 0, false},      // exactly 0 is not clamped
		{1000, 100, false}, // exactly 100 is not clamped
		{1001, 100, true},
		{-1, 0, true},
		{500, 50, false},
	} {
		if got, clamped := c.humidity(tc.raw); got != tc.want || clamped != tc.clamped {
			t.Errorf("humidity(%d) = %v (clamped %v), want %v (clamped %v)", tc.raw, got, clamped, tc.want, tc.clamped)
		}
	}
}

func TestTemperatureVectors(t *testing.T) {
	tests := []struct {
		raw  int16
		want float64
	}{
		{-200, 20}, // the first point
		{2200, 80}, // the second point
		{1000, 50}, // 20 + 1200 * 0.025
		{0, 25},    // 20 + 200 * 0.025
		{-2600, -40},
		{-2640, -41}, // below the operating range; the value is not clamped
		{3800, 120},
		{4200, 130},
	}
	for _, tc := range tests {
		if got := vectorCal.temperature(tc.raw); got != tc.want {
			t.Errorf("temperature(%d) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// A second, independent formulation: the Lagrange form of the same line,
// y = (y0 (x1 - x) + y1 (x - x0)) / (x1 - x0). It agrees with interpolate to
// rounding for any usable calibration and any count.
func TestInterpolationAgreesWithTheLagrangeForm(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 2000 {
		x0, x1 := int16(rng.Intn(65536)-32768), int16(rng.Intn(65536)-32768)
		if x0 == x1 {
			continue
		}
		y0, y1 := rng.Float64()*200-50, rng.Float64()*200-50
		x := int16(rng.Intn(65536) - 32768)
		got := interpolate(x, x0, x1, y0, y1)
		want := (y0*float64(int(x1)-int(x)) + y1*float64(int(x)-int(x0))) / float64(int(x1)-int(x0))
		if math.Abs(got-want) > 1e-9*(1+math.Abs(want)) {
			t.Fatalf("interpolate(%d; %d,%d; %v,%v) = %v, Lagrange form gives %v", x, x0, x1, y0, y1, got, want)
		}
	}
}

// Counts at the ends of the int16 range: their difference does not fit in an
// int16, which is where a narrow subtraction would wrap.
func TestInterpolationDoesNotOverflowOnWideCounts(t *testing.T) {
	c := Calibration{H0RH: 0, H1RH: 100, H0T0Out: -32768, H1T0Out: 32767, T0DegC: 0, T1DegC: 1, T0Out: 0, T1Out: 1}
	if got, _ := c.humidity(0); math.Abs(got-50.0007629) > 1e-6 {
		t.Errorf("humidity(0) = %v, want about 50.00076", got)
	}
	if got, _ := c.humidity(32767); got != 100 {
		t.Errorf("humidity(32767) = %v, want 100", got)
	}
	if got, _ := c.humidity(-32768); got != 0 {
		t.Errorf("humidity(-32768) = %v, want 0", got)
	}
}
