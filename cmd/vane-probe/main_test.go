package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

type rig struct {
	t    *testing.T
	root string
	bus  *vanetest.FakeBus
	hts  *vanetest.HTS221Model
	lps  *vanetest.LPS25HModel
}

type reporter struct{ t *testing.T }

func (r *reporter) Errorf(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf("protocol violation: "+format, args...)
}

func newRig(t *testing.T) *rig {
	t.Helper()
	rep := vanetest.WithReporter(&reporter{t})
	hts, lps := vanetest.NewHTS221(rep), vanetest.NewLPS25H(rep)
	return &rig{t: t, root: t.TempDir(), bus: vanetest.NewFakeBus(hts, lps), hts: hts, lps: lps}
}

func (r *rig) write(path, content string) {
	r.t.Helper()
	p := filepath.Join(r.root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// bindLPS makes the kernel own the LPS25H on bus 1.
func (r *rig) bindLPS() {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Join(r.root, "sys", "bus", "i2c", "devices", "1-005c"), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Symlink("../../../../bus/i2c/drivers/st-press-i2c", filepath.Join(r.root, "sys", "bus", "i2c", "devices", "1-005c", "driver")); err != nil {
		r.t.Fatal(err)
	}
}

// probe runs the command and returns its exit status, stdout and stderr.
func (r *rig) probe(args ...string) (code int, stdout, stderr string) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	code = run(append([]string{"-root", r.root}, args...), &out, &errOut, env{bus: r.bus})
	return code, out.String(), errOut.String()
}

func mustContain(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("the output lacks %q:\n%s", s, out)
		}
	}
}

func TestAHealthyBoardExitsZeroAndSaysSo(t *testing.T) {
	r := newRig(t)
	r.write("sys/class/i2c-dev/i2c-1/name", "bcm2835 (i2c@7e804000)\n")
	r.write("proc/device-tree/hat/product", "Raspberry Pi Sense HAT\x00")
	r.write("proc/device-tree/hat/vendor", "Raspberry Pi Foundation\x00")
	r.write("proc/device-tree/hat/product_ver", "0x0002\x00")
	code, out, errOut := r.probe("-require", "all")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q:\n%s", code, errOut, out)
	}
	mustContain(t, out,
		"/dev/i2c-1", "bcm2835 (i2c@7e804000)", "<- examined",
		"Raspberry Pi Sense HAT, Raspberry Pi Foundation, version 0x0002",
		"HTS221", "0x5f", "LPS25H", "0x5c", "identified",
		"2 of 2 chips usable")
	if strings.Contains(out, "Required but not usable") {
		t.Errorf("nothing is missing but the output says so:\n%s", out)
	}
}

// RF-14: a probe is for looking. Negative control, seen failing: a probe that
// opens the drivers writes to the chips.
func TestTheProbeNeverWritesToTheBoard(t *testing.T) {
	r := newRig(t)
	if code, _, _ := r.probe(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, tx := range r.bus.Log() {
		if len(tx.R) == 0 {
			t.Errorf("vane-probe wrote %x to %#x", tx.W, tx.Addr)
		}
	}
	if r.hts.Peek(0x20) != 0 || r.lps.Peek(0x20) != 0 {
		t.Error("a chip was powered up by a probe")
	}
}

func TestAKernelOwnedChipIsExplainedAndOnlyFailsWhenRequired(t *testing.T) {
	r := newRig(t)
	r.bindLPS()

	code, out, _ := r.probe()
	if code != 0 {
		t.Errorf("exit %d without -require: a chip the kernel owns is a finding, not a failure", code)
	}
	mustContain(t, out, `owned by the kernel driver "st-press-i2c"`, "vane does not touch a chip the kernel drives", "ADR-0001", "1 of 2 chips usable")

	code, out, _ = r.probe("-require", "lps25h")
	if code != 1 {
		t.Errorf("exit %d with the kernel-owned chip required, want 1", code)
	}
	mustContain(t, out, "Required but not usable: LPS25H")

	if code, _, _ := r.probe("-require", "hts221"); code != 0 {
		t.Errorf("exit %d requiring only the chip that works, want 0", code)
	}
	for _, tx := range r.bus.Log() {
		if tx.Addr == 0x5C {
			t.Error("the kernel-owned chip was addressed")
		}
	}
}

func TestEachKindOfProblemIsNamedAndAdvised(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*rig)
		want   []string
		absent string
	}{
		{"a chip that is not on the bus", func(r *rig) { r.bus = vanetest.NewFakeBus(r.hts) },
			[]string{"absent: nothing acknowledged the address", "HAT is seated"}, ""},
		{"a different chip answering", func(r *rig) {
			r.lps = vanetest.NewLPS25H(vanetest.WithWhoAmI(0xBC), vanetest.WithReporter(&reporter{r.t}))
			r.bus = vanetest.NewFakeBus(r.hts, r.lps)
		}, []string{"a different chip answered", "0xbc", "not the board vane expects"}, ""},
		{"a bus error", func(r *rig) { r.bus.Inject(0x5C, vanetest.EIO()) },
			[]string{"unreachable:", "the bus failed"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(r)
			code, out, _ := r.probe("-require", "all")
			if code != 1 {
				t.Errorf("exit %d, want 1 with the problem chip required", code)
			}
			mustContain(t, out, tc.want...)
			mustContain(t, out, "1 of 2 chips usable", "Required but not usable: LPS25H")
		})
	}
}

func TestABusThatWillNotOpenExitsOneAndSaysWhy(t *testing.T) {
	r := newRig(t)
	var out, errOut bytes.Buffer
	code := run([]string{"-root", r.root, "-bus", "/dev/i2c-251"}, &out, &errOut, env{})
	if code != 1 {
		t.Errorf("exit %d, want 1 for a bus that cannot be opened", code)
	}
	mustContain(t, out.String(), "cannot open /dev/i2c-251", "unreachable: the bus could not be opened (see above)")
	// The bus error is long and is said once, not once per chip.
	if n := strings.Count(out.String(), "cannot open /dev/i2c-251"); n != 1 {
		t.Errorf("the bus error appears %d times, want once:\n%s", n, out.String())
	}
	if strings.Contains(out.String(), "the bus failed; see the error above") {
		t.Errorf("a chip with no problem of its own was given advice about it:\n%s", out.String())
	}
}

func TestOptionsThatMakeNoSenseExitTwo(t *testing.T) {
	for name, args := range map[string][]string{
		"an unknown chip":                  {"-require", "bme280"},
		"an unknown flag":                  {"-nonsense"},
		"a bus the kernel check can't use": {"-bus", "fake-bus"},
	} {
		r := newRig(t)
		var out, errOut bytes.Buffer
		code := run(append([]string{"-root", r.root}, args...), &out, &errOut, env{bus: r.bus})
		if code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
		if errOut.Len() == 0 {
			t.Errorf("%s: nothing on stderr", name)
		}
		if len(r.bus.Log()) != 0 {
			t.Errorf("%s: the bus was used", name)
		}
	}
}

func TestRequireAcceptsTheNamesAndDeduplicates(t *testing.T) {
	r := newRig(t)
	r.bindLPS()
	for _, req := range []string{"lps25h", "LPS25HB", "all", "hts221, lps25h", "all,lps25h,lps25h"} {
		if code, _, _ := r.probe("-require", req); code != 1 {
			t.Errorf("-require %q: exit %d, want 1: it names the kernel-owned chip", req, code)
		}
	}
	if code, _, _ := r.probe("-require", "hts221,hts221"); code != 0 {
		t.Errorf("a repeated name changed the result: exit %d", code)
	}
	// A name given twice is one chip, and is reported once.
	_, out, _ := r.probe("-require", "lps25h,LPS25HB,all")
	if got := strings.Count(out, "LPS25H"); got != 2 { // its line in the table, and the one name in the summary
		t.Errorf("LPS25H appears %d times, want 2 (the table and one mention in the summary):\n%s", got, out)
	}
}

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-version"}, &out, &bytes.Buffer{}, env{}); code != 0 || !strings.Contains(out.String(), "vane-probe "+version) {
		t.Errorf("exit %d, output %q", code, out.String())
	}
}

// The EEPROM is a device on the bus and the output goes to a terminal.
// Negative control, seen failing: a control character passed through.
func TestHostileEEPROMTextCannotReachTheTerminal(t *testing.T) {
	r := newRig(t)
	r.write("proc/device-tree/hat/product", "Sense\x1b[2J\x1b[31mHAT\x07\x00")
	r.write("proc/device-tree/hat/vendor", "Evil\r\n\x1b]0;owned\x07\x00")
	_, out, _ := r.probe()
	for _, r := range out {
		if r == 0x1b || r == 0x07 || r == '\r' || (r < 0x20 && r != '\n') {
			t.Fatalf("the output holds the control character %U:\n%q", r, out)
		}
	}
	mustContain(t, out, "Sense")
}

func TestNoAdaptersAndNoHATAreSaidPlainly(t *testing.T) {
	r := newRig(t)
	_, out, _ := r.probe()
	mustContain(t, out, "none listed by the kernel", "not reported by the firmware", "That does not mean there is no HAT")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// A report that could not be written is a failure of the tool, however healthy
// the board. Negative control, seen failing: dropping the write error.
func TestAReportThatCannotBeWrittenExitsOne(t *testing.T) {
	r := newRig(t)
	var errOut bytes.Buffer
	code := run([]string{"-root", r.root}, failingWriter{}, &errOut, env{bus: r.bus})
	if code != 1 || !strings.Contains(errOut.String(), "writing the report") {
		t.Errorf("exit %d, stderr %q; want 1 and a message", code, errOut.String())
	}
}
