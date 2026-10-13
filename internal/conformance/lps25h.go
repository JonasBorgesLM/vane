package conformance

import (
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
)

// The LPS25H and LPS25HB, repeated from the datasheet as docs/hardware.md
// reproduces it.
const (
	lAddr   = 0x5C
	lWho    = 0x0F
	lCtrl1  = 0x20
	lCtrl2  = 0x21
	lStatus = 0x27
	lOut    = 0x28

	lPD      = 1 << 7
	lBDU     = 1 << 2
	lOneShot = 1 << 0
	lTDA     = 1 << 0
	lPDA     = 1 << 1
	lBothDA  = lTDA | lPDA
)

// LPS25H runs the checks against an LPS25H or LPS25HB at 0x5C on the target's bus.
func LPS25H(t *testing.T, tg Target) {
	t.Helper()
	d := dev{t: t, bus: tg.Bus, addr: lAddr}
	tg.prepare()
	t.Cleanup(func() { d.write(lCtrl1, lBDU) }) // leave the chip powered down
	quiesce := func() { d.write(lCtrl1, lBDU); d.write(lCtrl2, 0) }

	t.Run("identity", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: lAddr}
		for range 3 {
			if got := d.read(lWho); got != 0xBD {
				t.Errorf("WHO_AM_I = %#x, want 0xbd", got)
			}
		}
	})

	t.Run("one-shot protocol", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: lAddr}
		quiesce()
		d.write(lCtrl1, lPD|lBDU)
		d.write(lCtrl2, lOneShot)

		// ASSUMPTION: ONE_SHOT reads back 1 while the conversion is pending. The
		// driver's recovery after a timeout relies on it. Observed, not asserted: a
		// fast conversion could finish before this read.
		pending := d.read(lCtrl2)&lOneShot != 0
		observe(t, "ONE_SHOT read back %v immediately after the trigger (the driver's recovery assumes true)", pending)

		if !d.waitFor(tg, lStatus, lBothDA) {
			t.Fatalf("a one-shot conversion did not finish within %v: STATUS_REG = %#x", tg.timeout(), d.read(lStatus))
		}
		if d.read(lCtrl2)&lOneShot != 0 {
			t.Errorf("ONE_SHOT is still set after the conversion finished: CTRL_REG2 = %#x. "+
				"The driver and the model both assume it clears itself", d.read(lCtrl2))
		}

		// The five output bytes are read as one block, as the driver reads them.
		// ASSUMPTION: that clears both data-ready bits. Overrun bits are observed.
		out := d.block(lOut, 5)
		st := d.read(lStatus)
		if st&lBothDA != 0 {
			t.Errorf("a data-ready flag is still set after the whole output block was read: STATUS_REG = %#x", st)
		}
		observe(t, "STATUS_REG after reading the output block: %#x (overrun bits: P_OR=%v T_OR=%v)", st, st&(1<<5) != 0, st&(1<<4) != 0)
		observe(t, "output block: %x", out)
	})

	t.Run("one-shot is ignored while powered down", func(t *testing.T) {
		d := dev{t: t, bus: tg.Bus, addr: lAddr}
		quiesce()
		d.write(lCtrl2, lOneShot)
		if !d.stays(tg, lStatus, lBothDA, 100*time.Millisecond) {
			t.Errorf("a conversion ran while the chip was powered down. The model assumes ONE_SHOT is ignored then, " +
				"and the driver's reset detection (ErrNotReady) relies on it")
		}
		observe(t, "CTRL_REG2 after ONE_SHOT was written to a powered-down chip: %#x", d.read(lCtrl2))
		d.write(lCtrl2, 0)
	})

	t.Run("the driver, one-shot", func(t *testing.T) {
		quiesce()
		s, err := lps25h.New(bg, tg.Bus, lps25h.WithBusName("conformance"))
		if err != nil {
			t.Fatal(err)
		}
		d := dev{t: t, bus: tg.Bus, addr: lAddr}
		if got := d.read(lCtrl1); got != lPD|lBDU {
			t.Errorf("CTRL_REG1 after New = %#x, want %#x (powered up, BDU on, one-shot)", got, lPD|lBDU)
		}
		var last time.Time
		for i := range 3 {
			sample, err := s.Read(bg)
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			if p := sample.Pressure; p.Flags != 0 || p.Value < 260 || p.Value > 1260 {
				t.Errorf("read %d: pressure %v flags %q", i, p.Value, p.Flags)
			}
			if c := sample.Temperature; c.Flags != 0 || c.Value < -10 || c.Value > 90 {
				t.Errorf("read %d: temperature %v flags %q", i, c.Value, c.Flags)
			}
			if !sample.Pressure.Time.After(last) {
				t.Errorf("read %d: the time did not advance", i)
			}
			last = sample.Pressure.Time
			observe(t, "read %d: %.2f hPa (raw %d), %.2f degC (raw %d)", i,
				float64(sample.Pressure.Value), sample.Pressure.Raw, float64(sample.Temperature.Value), sample.Temperature.Raw)
		}
		if err := s.Close(bg); err != nil {
			t.Fatal(err)
		}
		if got := d.read(lCtrl1); got != lBDU {
			t.Errorf("CTRL_REG1 after Close = %#x, want %#x (powered down, BDU still on)", got, lBDU)
		}
	})

	t.Run("the driver, continuous", func(t *testing.T) {
		quiesce()
		s, err := lps25h.New(bg, tg.Bus, lps25h.WithMode(lps25h.Continuous(lps25h.Rate1Hz)))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(bg)
		tg.advance(1200 * time.Millisecond) // more than one period at 1 Hz
		fresh, err := s.Read(bg)
		if err != nil {
			t.Fatal(err)
		}
		if fresh.Pressure.Flags.Has(vane.FlagStale) || fresh.Temperature.Flags.Has(vane.FlagStale) {
			t.Errorf("a read after more than a period is stale: %q, %q", fresh.Pressure.Flags, fresh.Temperature.Flags)
		}
		again, err := s.Read(bg) // well inside the next period
		if err != nil {
			t.Fatal(err)
		}
		if !again.Pressure.Flags.Has(vane.FlagStale) || !again.Temperature.Flags.Has(vane.FlagStale) {
			t.Errorf("an immediate second read is not stale: %q, %q. FlagStale rests on the data-ready bits clearing when the outputs are read",
				again.Pressure.Flags, again.Temperature.Flags)
		}
	})
}
