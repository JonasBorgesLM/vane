//go:build hardware

package conformance_test

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/i2cdev"
	"github.com/JonasBorgesLM/vane/internal/conformance"
	"github.com/JonasBorgesLM/vane/internal/kernel"
	"github.com/JonasBorgesLM/vane/sensehat"
)

// This file runs on a Raspberry Pi with a Sense HAT, never in CI:
//
//	VANE_I2C_BUS    the adapter device, /dev/i2c-1 by default
//	VANE_EXPECT     "custom" when the board is set up for vane (no kernel driver
//	                on the chips): the raw checks must then run, and skipping them
//	                is a failure. "default" when the stock overlay is loaded: the
//	                chips must be reported kernel-owned and left untouched.
//	                Unset: run what can be run and skip the rest.
//
// The raw checks write to the chips, so they never run on a chip a kernel driver
// owns (I2C_RDWR checks nothing, see ADR-0003).

func busPath() string {
	if p := os.Getenv("VANE_I2C_BUS"); p != "" {
		return p
	}
	return "/dev/i2c-1"
}

func report(t *testing.T) sensehat.Report {
	t.Helper()
	rep, err := sensehat.Diagnose(context.Background(), sensehat.WithBusPath(busPath()))
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	t.Logf("bus %s, board %+v", rep.Bus, rep.Board)
	for _, c := range rep.Chips {
		t.Logf("%s: %s %s (err: %v)", c.Source, c.Finding, c.Driver, c.Err)
	}
	return rep
}

func finding(rep sensehat.Report, chip vane.Chip) (sensehat.ChipReport, bool) {
	for _, c := range rep.Chips {
		if c.Source.Chip == chip {
			return c, true
		}
	}
	return sensehat.ChipReport{}, false
}

// runnable decides whether the raw checks may run on a chip, from what Diagnose
// found and what the operator said to expect.
func runnable(t *testing.T, rep sensehat.Report, chip vane.Chip) {
	t.Helper()
	c, ok := finding(rep, chip)
	if !ok {
		t.Fatalf("Diagnose has no entry for %v", chip)
	}
	switch os.Getenv("VANE_EXPECT") {
	case "default":
		if c.Finding != sensehat.FindingKernelOwned {
			t.Fatalf("VANE_EXPECT=default: %v is %v, want it owned by the kernel driver", chip, c.Finding)
		}
		t.Skipf("%v is owned by %q: not touched", chip, c.Driver)
	case "custom":
		if !c.Usable() {
			t.Fatalf("VANE_EXPECT=custom: %v is %v (%v), want it free for vane", chip, c.Finding, c.Err)
		}
	default:
		if !c.Usable() {
			t.Skipf("%v is %v: not touched", chip, c.Finding)
		}
	}
}

func target(t *testing.T) conformance.Target {
	t.Helper()
	bus, err := i2cdev.Open(busPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close() })
	return conformance.Target{Bus: bus, Advance: time.Sleep, Timeout: 2 * time.Second}
}

func TestHardwareDiagnose(t *testing.T) {
	rep := report(t)
	if rep.BusErr != nil {
		t.Fatalf("the bus could not be opened: %v", rep.BusErr)
	}
	if os.Getenv("VANE_EXPECT") == "custom" {
		if m := rep.Missing(); len(m) > 0 {
			t.Errorf("chips vane cannot use: %v", m)
		}
	}
}

func TestHardwareHTS221(t *testing.T) {
	runnable(t, report(t), vane.ChipHTS221)
	conformance.HTS221(t, target(t))
}

func TestHardwareLPS25H(t *testing.T) {
	runnable(t, report(t), vane.ChipLPS25H)
	conformance.LPS25H(t, target(t))
}

// TestHardwareLSM9DS1 checks the IMU, which Diagnose does not cover yet: the
// kernel ownership of both its addresses is read from sysfs first, because the
// checks write to the chip and I2C_RDWR does not stop that reaching a chip a
// kernel driver owns (docs/hardware.md, #15).
func TestHardwareLSM9DS1(t *testing.T) {
	n, ok := kernel.BusNumber(busPath())
	if !ok {
		t.Fatalf("%s does not name an I2C adapter", busPath())
	}
	for _, addr := range []uint16{0x6A, 0x1C} {
		drv, owned, err := kernel.Owner("/", n, addr)
		switch {
		case err != nil:
			t.Fatalf("kernel ownership of 0x%02x: %v", addr, err)
		case owned && os.Getenv("VANE_EXPECT") == "custom":
			t.Fatalf("VANE_EXPECT=custom: 0x%02x is owned by %q, want it free for vane", addr, drv)
		case owned:
			t.Skipf("0x%02x is owned by %q: not touched", addr, drv)
		}
	}
	conformance.LSM9DS1(t, target(t))
}

// TestHardwareLatency records how long a one-shot Read takes, which docs/hardware.md
// asks for and the README quotes. It asserts nothing about the numbers.
func TestHardwareLatency(t *testing.T) {
	rep := report(t)
	ctx := context.Background()
	const n = 20
	measure := func(name string, read func() error) {
		d := make([]time.Duration, 0, n)
		for range n {
			start := time.Now()
			if err := read(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			d = append(d, time.Since(start))
		}
		slices.SortFunc(d, cmp.Compare[time.Duration])
		t.Logf("LATENCY %s one-shot Read over %d: min %v, median %v, max %v", name, n, d[0], d[n/2], d[n-1])
	}
	if c, ok := finding(rep, vane.ChipHTS221); ok && c.Usable() {
		// Every averaging setting, in step: the conversion time grows with it, and
		// the default (32/16) is the fourth. The LPS25H offers no averaging option
		// while its sample counts are in conflict (docs/hardware.md).
		bus := target(t).Bus
		for h, tmp := uint16(4), uint16(2); h <= 512; h, tmp = h*2, tmp*2 {
			avg := hts221.Averaging{Humidity: h, Temperature: tmp}
			s, err := hts221.New(ctx, bus, hts221.WithAveraging(avg))
			if err != nil {
				t.Fatal(err)
			}
			measure(fmt.Sprintf("HTS221 averaging %d/%d", h, tmp), func() error { _, err := s.Read(ctx); return err })
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if c, ok := finding(rep, vane.ChipLPS25H); ok && c.Usable() {
		// Pressure averaging dominates the conversion, so it is swept fully and
		// temperature at its ends. The last setting is 512/64, the LPS25HB's
		// reset value: Close does not restore RES_CONF, and the next user of the
		// chip should find it as the board ships.
		bus := target(t).Bus
		for _, tmp := range []uint16{8, 64} {
			for _, p := range []uint16{8, 32, 128, 512} {
				avg := lps25h.Averaging{Pressure: p, Temperature: tmp}
				s, err := lps25h.New(ctx, bus, lps25h.WithAveraging(avg))
				if err != nil {
					t.Fatal(err)
				}
				measure(fmt.Sprintf("LPS25H averaging %d/%d", p, tmp), func() error { _, err := s.Read(ctx); return err })
				if err := s.Close(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
