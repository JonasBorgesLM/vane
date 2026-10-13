package conformance

import (
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
)

// The HTS221, repeated from the datasheet as docs/hardware.md reproduces it.
const (
	hAddr   = 0x5F
	hWho    = 0x0F
	hAv     = 0x10
	hCtrl1  = 0x20
	hCtrl2  = 0x21
	hStatus = 0x27
	hOut    = 0x28
	hCal    = 0x30

	hPD      = 1 << 7
	hBDU     = 1 << 2
	hOneShot = 1 << 0
	hTDA     = 1 << 0
	hHDA     = 1 << 1
	hBothDA  = hTDA | hHDA
)

// HTS221 runs the checks against an HTS221 at 0x5F on the target's bus.
func HTS221(t *testing.T, tg Target) {
	t.Helper()
	d := dev{t: t, bus: tg.Bus, addr: hAddr}
	tg.prepare()
	t.Cleanup(func() { d.write(hCtrl1, hBDU) }) // leave the chip powered down
	quiesce := func() { d.write(hCtrl1, hBDU); d.write(hCtrl2, 0) }

	t.Run("identity", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: hAddr}
		for range 3 {
			if got := d.read(hWho); got != 0xBC {
				t.Errorf("WHO_AM_I = %#x, want 0xbc", got)
			}
		}
	})

	t.Run("calibration block", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: hAddr}
		first, second := d.block(hCal, 16), d.block(hCal, 16)
		for i := range first {
			if first[i] != second[i] {
				t.Errorf("register 0x%02x read %#x then %#x: the calibration block is not stable", hCal+i, first[i], second[i])
			}
		}
		// ASSUMPTION (docs/hardware.md, still to confirm): reading the reserved
		// registers 0x34, 0x38 and 0x39 inside the block is harmless. The check above
		// is what shows it: the read succeeded and was stable.
		observe(t, "the 16-byte calibration block 0x30..0x3f reads cleanly and is stable: %x", first)
		s, err := hts221.New(bg, tg.Bus)
		if err != nil {
			t.Fatalf("hts221.New: %v", err)
		}
		defer s.Close(bg)
		cal := s.Calibration()
		if err := cal.Validate(); err != nil {
			t.Errorf("the chip's calibration is unusable: %v", err)
		}
		if cal.H0RH >= cal.H1RH || cal.T0DegC >= cal.T1DegC {
			t.Errorf("calibration points are not ordered: %+v", cal)
		}
	})

	t.Run("one-shot protocol", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: hAddr}
		quiesce()
		d.write(hCtrl1, hPD|hBDU)
		d.write(hCtrl2, hOneShot)

		// ASSUMPTION: ONE_SHOT reads back 1 while the conversion is pending. The
		// driver's recovery after a timeout relies on it. Observed, not asserted: a
		// fast conversion could finish before this read.
		pending := d.read(hCtrl2)&hOneShot != 0
		observe(t, "ONE_SHOT read back %v immediately after the trigger (the driver's recovery assumes true)", pending)

		if !d.waitFor(tg, hStatus, hBothDA) {
			t.Fatalf("a one-shot conversion did not finish within %v: STATUS_REG = %#x", tg.timeout(), d.read(hStatus))
		}
		if d.read(hCtrl2)&hOneShot != 0 {
			t.Errorf("ONE_SHOT is still set after the conversion finished: CTRL_REG2 = %#x. "+
				"The driver and the model both assume it clears itself", d.read(hCtrl2))
		}

		// ASSUMPTION: a data-ready flag clears once both bytes of its output have
		// been read. Asserted in the weak form that holds whichever byte the chip
		// keys on; the strict form is observed.
		d.block(hOut, 2) // both humidity bytes
		afterH := d.read(hStatus)
		if afterH&hHDA != 0 {
			t.Errorf("H_DA is still set after both humidity bytes were read: STATUS_REG = %#x", afterH)
		}
		observe(t, "after reading both humidity bytes, T_DA is still set: %v (it should be: the temperature was not read)", afterH&hTDA != 0)
		d.block(hOut+2, 2)
		if got := d.read(hStatus); got&hBothDA != 0 {
			t.Errorf("a data-ready flag is still set after every output byte was read: STATUS_REG = %#x", got)
		}

		// Does one byte clear it? The strict model says no.
		d.write(hCtrl2, hOneShot)
		d.waitFor(tg, hStatus, hBothDA)
		d.read(hOut)
		oneByte := d.read(hStatus)&hHDA == 0
		observe(t, "H_DA cleared by reading only HUMIDITY_OUT_L: %v (the model assumes false)", oneByte)
		d.block(hOut, 4)
	})

	t.Run("one-shot is ignored while powered down", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: hAddr}
		quiesce()
		d.write(hCtrl2, hOneShot)
		if !d.stays(tg, hStatus, hBothDA, 100*time.Millisecond) {
			t.Errorf("a conversion ran while the chip was powered down. The model assumes ONE_SHOT is ignored then, " +
				"and the driver's reset detection (ErrNotReady) relies on it")
		}
		observe(t, "CTRL_REG2 after ONE_SHOT was written to a powered-down chip: %#x (the model clears the bit at once)", d.read(hCtrl2))
	})

	t.Run("the driver, one-shot", func(t *testing.T) {
		quiesce()
		s, err := hts221.New(bg, tg.Bus, hts221.WithBusName("conformance"))
		if err != nil {
			t.Fatal(err)
		}
		d := dev{t: t, bus: tg.Bus, addr: hAddr}
		if got := d.read(hCtrl1); got != hPD|hBDU {
			t.Errorf("CTRL_REG1 after New = %#x, want %#x (powered up, BDU on, one-shot)", got, hPD|hBDU)
		}
		var last time.Time
		for i := range 3 {
			sample, err := s.Read(bg)
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			if h := sample.Humidity; h.Flags != 0 || h.Value < 0 || h.Value > 100 {
				t.Errorf("read %d: humidity %v flags %q", i, h.Value, h.Flags)
			}
			if c := sample.Temperature; c.Flags != 0 || c.Value < -10 || c.Value > 90 {
				t.Errorf("read %d: temperature %v flags %q", i, c.Value, c.Flags)
			}
			// Before, not !After: a coarse clock (Windows) gives two quick reads one time.
			if sample.Humidity.Time.Before(last) {
				t.Errorf("read %d: the time went backwards", i)
			}
			last = sample.Humidity.Time
			observe(t, "read %d: %.2f %%RH (raw %d), %.2f degC (raw %d)", i,
				float64(sample.Humidity.Value), sample.Humidity.Raw, float64(sample.Temperature.Value), sample.Temperature.Raw)
		}
		if err := s.Close(bg); err != nil {
			t.Fatal(err)
		}
		if got := d.read(hCtrl1); got != hBDU {
			t.Errorf("CTRL_REG1 after Close = %#x, want %#x (powered down, BDU still on)", got, hBDU)
		}
	})

	t.Run("the driver, continuous", func(t *testing.T) {
		quiesce()
		s, err := hts221.New(bg, tg.Bus, hts221.WithMode(hts221.Continuous(hts221.Rate1Hz)))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(bg)
		tg.advance(1200 * time.Millisecond) // more than one period at 1 Hz
		fresh, err := s.Read(bg)
		if err != nil {
			t.Fatal(err)
		}
		if fresh.Humidity.Flags.Has(vane.FlagStale) || fresh.Temperature.Flags.Has(vane.FlagStale) {
			t.Errorf("a read after more than a period is stale: %q, %q", fresh.Humidity.Flags, fresh.Temperature.Flags)
		}
		again, err := s.Read(bg) // well inside the next period
		if err != nil {
			t.Fatal(err)
		}
		if !again.Humidity.Flags.Has(vane.FlagStale) || !again.Temperature.Flags.Has(vane.FlagStale) {
			t.Errorf("an immediate second read is not stale: %q, %q. FlagStale rests on the data-ready bits clearing when the outputs are read",
				again.Humidity.Flags, again.Temperature.Flags)
		}
	})
}
