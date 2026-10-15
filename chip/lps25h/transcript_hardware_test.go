//go:build hardware && linux

package lps25h_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/i2cdev"
	"github.com/JonasBorgesLM/vane/internal/kernel"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// TestRecordTranscript records transcriptScenario on a real LPS25H, on a Pi,
// never in CI:
//
//	VANE_I2C_BUS  the adapter device, /dev/i2c-1 by default
//	VANE_RECORD   the file to write, such as
//	              testdata/transcripts/lps25h-pi5-sensehat-v2.txt
//
// The file is written only when the whole scenario succeeded, so a failed run
// leaves no transcript that a replay would then hold the driver to.
func TestRecordTranscript(t *testing.T) {
	out := os.Getenv("VANE_RECORD")
	if out == "" {
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
	// The scenario writes to the chip, and I2C_RDWR does not stop it reaching a
	// chip a kernel driver owns (docs/hardware.md, #15): the sysfs check is the
	// only guard, so it runs first.
	switch drv, owned, err := kernel.Owner("/", n, lps25h.DefaultAddress); {
	case err != nil:
		t.Fatalf("kernel ownership of 0x%02x: %v", lps25h.DefaultAddress, err)
	case owned:
		t.Skipf("the LPS25H is owned by the kernel driver %q: not touched", drv)
	}

	bus, err := i2cdev.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()

	var buf bytes.Buffer
	rec := vanetest.NewRecorder(bus, &buf)
	rec.Comment("LPS25H at 0x%02x on %s; scenario: New, %d one-shot reads, Close", lps25h.DefaultAddress, path, transcriptReads)
	rec.Comment("recorded %s", time.Now().UTC().Format(time.RFC3339))
	if rel, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		rec.Comment("kernel %s", strings.TrimSpace(string(rel)))
	}
	if product, vendor, ver, ok := kernel.Board("/"); ok {
		rec.Comment("board %s, %s, version %s", product, vendor, ver)
	}

	got, err := transcriptScenario(bg, rec)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	for _, line := range got {
		rec.Comment("%s", strings.TrimPrefix(expectPrefix, "# ")+line)
	}
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s:\n%s", out, buf.String())
}
