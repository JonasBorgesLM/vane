package lps25h_test

import (
	"errors"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
)

// RES_CONF: AVGT in bits 3:2, AVGP in bits 1:0, both 00 to 11 (LPS25HB
// DocID027112 Rev 4 p. 34; the bit positions measured on the board, see
// docs/hardware.md). The model starts at the LPS25H's reset value, 0x05.
func TestWithAveragingWritesResConf(t *testing.T) {
	for name, tc := range map[string]struct {
		avg  lps25h.Averaging
		want uint8
	}{
		"most":                     {lps25h.Averaging{Pressure: 512, Temperature: 64}, 0x0F},
		"least":                    {lps25h.Averaging{Pressure: 8, Temperature: 8}, 0x00},
		"mixed":                    {lps25h.Averaging{Pressure: 32, Temperature: 32}, 0x09},
		"pressure only keeps temp": {lps25h.Averaging{Pressure: 128}, 0x06},
		"temp only keeps pressure": {lps25h.Averaging{Temperature: 64}, 0x0D},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			newSensor(t, r, lps25h.WithAveraging(tc.avg))
			if got := r.model.Peek(0x10); got != tc.want {
				t.Errorf("RES_CONF = %#x, want %#x", got, tc.want)
			}
		})
	}
}

func TestAnInvalidAveragingTouchesNothing(t *testing.T) {
	for _, a := range []lps25h.Averaging{{Pressure: 16}, {Pressure: 1024}, {Temperature: 128}, {Temperature: 4}, {Pressure: 7}} {
		r := newRig(t)
		if _, err := lps25h.New(bg, r.bus, lps25h.WithAveraging(a)); err == nil {
			t.Errorf("%+v accepted", a)
		}
		if n := len(r.bus.Log()); n != 0 {
			t.Errorf("%+v: %d transactions before the option was refused", a, n)
		}
	}
}

// A reset puts RES_CONF back to the chip's default; the reinit after it must
// apply the averaging again, or the sensor silently changes resolution.
func TestAveragingSurvivesAChipReset(t *testing.T) {
	r := newRig(t)
	s := newSensor(t, r, lps25h.WithAveraging(lps25h.Averaging{Pressure: 8, Temperature: 8}), lps25h.WithReadyTimeout(20*time.Millisecond))
	r.model.Reset()
	if got := r.model.Peek(0x10); got != 0x05 {
		t.Fatalf("the model did not reset RES_CONF: %#x", got)
	}
	if _, err := s.Read(bg); !errors.Is(err, vane.ErrNotReady) {
		t.Fatalf("read after a reset: %v", err)
	}
	mustRead(t, s)
	if got := r.model.Peek(0x10); got != 0x00 {
		t.Errorf("RES_CONF after the reinit = %#x, want 0x00", got)
	}
}
