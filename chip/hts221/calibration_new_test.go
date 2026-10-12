package hts221_test

import (
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

func TestNewReadsTheFactoryCalibrationFromTheChip(t *testing.T) {
	r := newRig(t)
	s, err := hts221.New(bg, r.bus)
	if err != nil {
		t.Fatal(err)
	}
	f := r.model.Factory() // the model's own numbers, written separately from the parser
	want := hts221.Calibration{
		H0RH: f.H0RH, H1RH: f.H1RH, T0DegC: f.T0DegC, T1DegC: f.T1DegC,
		H0T0Out: f.H0T0Out, H1T0Out: f.H1T0Out, T0Out: f.T0Out, T1Out: f.T1Out,
	}
	if got := s.Calibration(); got != want {
		t.Errorf("Calibration() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestTheCalibrationIsOneBlockReadWithAutoIncrement(t *testing.T) {
	r := newRig(t)
	if _, err := hts221.New(bg, r.bus); err != nil {
		t.Fatal(err)
	}
	log := r.bus.Log()
	if len(log) < 2 || len(log[1].W) != 1 || log[1].W[0] != 0x30|0x80 || len(log[1].R) != 16 {
		t.Fatalf("the second transaction is not a 16-byte block read at 0xb0:\n%s", describe(log))
	}
}

// Negative control, seen failing: not calling Validate in calibrate lets a
// degenerate chip through and the configuration writes happen.
func TestADegenerateCalibrationIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	for name, poke := range map[string]func(*vanetest.HTS221Model){
		"both humidity counts the same, as reported on real chips": func(m *vanetest.HTS221Model) {
			m.Poke(0x3A, m.Peek(0x36))
			m.Poke(0x3B, m.Peek(0x37))
		},
		"both temperature counts the same": func(m *vanetest.HTS221Model) {
			m.Poke(0x3E, m.Peek(0x3C))
			m.Poke(0x3F, m.Peek(0x3D))
		},
		"every calibration register zero": func(m *vanetest.HTS221Model) {
			for reg := uint8(0x30); reg <= 0x3F; reg++ {
				m.Poke(reg, 0)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			poke(r.model)
			s, err := hts221.New(bg, r.bus)
			if s != nil {
				t.Fatal("New returned a sensor with an unusable calibration")
			}
			var se *vane.SensorError
			if !errors.Is(err, vane.ErrCalibration) || !errors.As(err, &se) || se.Op != "calibrate" || se.Reg != 0x30 || !se.HasReg {
				t.Fatalf("error = %v, want a SensorError for op calibrate wrapping ErrCalibration", err)
			}
			if w := writes(r.bus); len(w) != 0 {
				t.Fatalf("%d writes reached a chip whose calibration is unusable:\n%s", len(w), describe(r.bus.Log()))
			}
		})
	}
}

func TestACalibrationReadThatFailsIsReportedAsSuch(t *testing.T) {
	for name, tc := range map[string]struct {
		fault vanetest.Fault
		want  error
	}{
		"an I/O error": {vanetest.EIO().After(1), vanetest.ErrIO},
		"a short read": {vanetest.ShortRead().After(1), vanetest.ErrShortRead},
		"a NACK":       {vanetest.NACK().After(1), vane.ErrNotPresent},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.bus.Inject(0x5F, tc.fault) // After(1): the identity read passes
			_, err := hts221.New(bg, r.bus)
			var se *vane.SensorError
			if !errors.As(err, &se) || se.Op != "calibrate" || !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want a calibrate SensorError wrapping %v", err, tc.want)
			}
			if w := writes(r.bus); len(w) != 0 {
				t.Errorf("%d writes after a failed calibration read", len(w))
			}
		})
	}
}
