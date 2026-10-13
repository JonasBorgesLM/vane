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
