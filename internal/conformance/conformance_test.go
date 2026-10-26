package conformance_test

import (
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane/internal/conformance"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// The models must pass the checks that a real chip is held to. A failure here is
// a model that disagrees with what the checks say any correct chip does.

func TestHTS221Model(t *testing.T) {
	m := vanetest.NewHTS221()
	conformance.HTS221(t, conformance.Target{
		Bus:     vanetest.NewFakeBus(m),
		Prepare: func() { m.Set(45, 25) },
		Advance: func(time.Duration) { m.NewSample() },
	})
}

func TestLPS25HModel(t *testing.T) {
	m := vanetest.NewLPS25H()
	conformance.LPS25H(t, conformance.Target{
		Bus:     vanetest.NewFakeBus(m),
		Prepare: func() { m.Set(1013, 25) },
		Advance: func(time.Duration) { m.NewSample() },
	})
}

func TestLSM9DS1Model(t *testing.T) {
	ag, mag := vanetest.NewLSM9DS1AccelGyro(), vanetest.NewLSM9DS1Magnetometer()
	var agTime, magTime time.Duration
	conformance.LSM9DS1(t, conformance.Target{
		Bus: vanetest.NewFakeBus(ag, mag),
		Prepare: func() {
			ag.Set(vanetest.Axes{10, 20, 30}, vanetest.Axes{-1, -2, -3})
			mag.Set(vanetest.Axes{4, 5, 6})
		},
		// The model has no clock: time is spent in ODR periods, at the rates the
		// checks configure (119 Hz, and the magnetometer's reset 10 Hz).
		Advance: func(d time.Duration) {
			for agTime += d; agTime >= time.Second/119; agTime -= time.Second / 119 {
				ag.Tick()
			}
			for magTime += d; magTime >= 100*time.Millisecond; magTime -= 100 * time.Millisecond {
				mag.Tick()
			}
		},
	})
}

func TestTCS3400Model(t *testing.T) {
	m := vanetest.NewTCS3400(vanetest.WithConversionPolls(3))
	conformance.TCS3400(t, conformance.Target{
		Bus:     vanetest.NewFakeBus(m),
		Prepare: func() { m.Set(120, 57, 66, 60) },
	}, 0x39)
}
