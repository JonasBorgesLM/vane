//go:build hardware && linux

package lsm9ds1_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane/i2cdev"
	"github.com/JonasBorgesLM/vane/internal/kernel"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// TestRecordTranscript records each scenario on a real LSM9DS1, on a Pi, never
// in CI:
//
//	VANE_I2C_BUS  the adapter device, /dev/i2c-1 by default
//	VANE_RECORD   a directory; each scenario is written to lsm9ds1-pi5-<name>.txt
//
// A file is written only when its scenario succeeded.
func TestRecordTranscript(t *testing.T) {
	dir := os.Getenv("VANE_RECORD")
	if dir == "" {
		t.Skip("VANE_RECORD is not set")
	}
	path := os.Getenv("VANE_I2C_BUS")
	if path == "" {
		path = "/dev/i2c-1"
	}
	n, ok := kernel.BusNumber(path)
	if !ok {
		t.Fatalf("%s does not name an I2C adapter", path)
	}
	// The scenarios write to the chip, and I2C_RDWR does not stop that reaching
	// a chip a kernel driver owns (docs/hardware.md, #15).
	for _, addr := range []uint16{0x6A, 0x1C} {
		switch drv, owned, err := kernel.Owner("/", n, addr); {
		case err != nil:
			t.Fatalf("kernel ownership of 0x%02x: %v", addr, err)
		case owned:
			t.Skipf("0x%02x is owned by the kernel driver %q: not touched", addr, drv)
		}
	}
	bus, err := i2cdev.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()

	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			rec := vanetest.NewRecorder(bus, &buf)
			rec.Comment("scenario %s", name)
			rec.Comment("LSM9DS1 at 0x6a and 0x1c on %s", path)
			rec.Comment("recorded %s", time.Now().UTC().Format(time.RFC3339))
			if rel, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
				rec.Comment("kernel %s", strings.TrimSpace(string(rel)))
			}
			if product, vendor, ver, ok := kernel.Board("/"); ok {
				rec.Comment("board %s, %s, version %s", product, vendor, ver)
			}
			got, err := run(bg, rec, time.Sleep)
			if err != nil {
				t.Fatalf("scenario: %v", err)
			}
			for _, line := range got {
				rec.Comment("%s", strings.TrimPrefix(expectPrefix, "# ")+line)
			}
			if err := rec.Err(); err != nil {
				t.Fatal(err)
			}
			out := dir + "/lsm9ds1-pi5-" + name + ".txt"
			if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s (%d lines)", out, bytes.Count(buf.Bytes(), []byte("\n")))
		})
	}
}
