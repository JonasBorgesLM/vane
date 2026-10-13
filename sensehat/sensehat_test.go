package sensehat_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
	"github.com/JonasBorgesLM/vane/sensehat"
	"github.com/JonasBorgesLM/vane/vanetest"
)

var bg = context.Background()

var fixedTime = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return fixedTime }

type reporter struct{ t *testing.T }

func (r *reporter) Errorf(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf("protocol violation: "+format, args...)
}

// board is a Sense HAT of models on a fake bus, with a sysfs and device tree in
// a temporary directory.
type board struct {
	t    *testing.T
	hts  *vanetest.HTS221Model
	lps  *vanetest.LPS25HModel
	bus  *vanetest.FakeBus
	root string
}

func newBoard(t *testing.T) *board {
	t.Helper()
	rep := vanetest.WithReporter(&reporter{t})
	hts, lps := vanetest.NewHTS221(rep), vanetest.NewLPS25H(rep)
	hts.Set(50, 25)
	lps.Set(1013.25, 22.5)
	return &board{t: t, hts: hts, lps: lps, bus: vanetest.NewFakeBus(hts, lps), root: t.TempDir()}
}

// bind makes the kernel own the chip at addr on bus 1, as the rpi-sense overlay
// does for the LPS25H.
func (b *board) bind(addr uint16, driver string) {
	b.t.Helper()
	node := map[uint16]string{0x5C: "1-005c", 0x5F: "1-005f"}[addr]
	dir := filepath.Join(b.root, "sys", "bus", "i2c", "devices", node)
	drivers := filepath.Join(b.root, "sys", "bus", "i2c", "drivers", driver)
	for _, d := range []string{dir, drivers} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			b.t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "..", "..", "..", "bus", "i2c", "drivers", driver), filepath.Join(dir, "driver")); err != nil {
		b.t.Fatal(err)
	}
}

func (b *board) open(opts ...sensehat.Option) (*sensehat.Device, error) {
	b.t.Helper()
	all := make([]sensehat.Option, 0, 5+len(opts))
	all = append(all,
		sensehat.WithBus(b.bus),
		sensehat.WithRoot(b.root),
		sensehat.WithClock(fixedClock),
		sensehat.WithHTS221(hts221.WithPollInterval(time.Millisecond)),
		sensehat.WithLPS25H(lps25h.WithPollInterval(time.Millisecond)))
	return sensehat.Open(bg, append(all, opts...)...)
}

func (b *board) mustOpen(opts ...sensehat.Option) *sensehat.Device {
	b.t.Helper()
	d, err := b.open(opts...)
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { _ = d.Close() })
	return d
}

// touched reports whether anything reached the chip at addr.
func (b *board) touched(addr uint16) bool {
	for _, tx := range b.bus.Log() {
		if tx.Addr == addr {
			return true
		}
	}
	return false
}

func states(d *sensehat.Device) map[vane.Chip]sensehat.State {
	out := map[vane.Chip]sensehat.State{}
	for _, s := range d.Sensors() {
		out[s.Source.Chip] = s.State
	}
	return out
}

func TestOpenFindsBothChipsAndReadsThemTogether(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen()
	got := states(d)
	if got[vane.ChipHTS221] != sensehat.StateReady || got[vane.ChipLPS25H] != sensehat.StateReady {
		t.Fatalf("states = %v, want both ready", got)
	}
	env, err := d.Environment(bg)
	if err != nil {
		t.Fatal(err)
	}
	if env.Humidity.Err != nil || env.Humidity.Raw != 3500 || env.Humidity.Time != fixedTime {
		t.Errorf("Humidity = %+v", env.Humidity)
	}
	if env.Pressure.Err != nil || env.Pressure.Raw != 4150272 || env.Pressure.Source.Address != 0x5C {
		t.Errorf("Pressure = %+v", env.Pressure)
	}
	if len(env.Temperatures) != 2 {
		t.Fatalf("Temperatures = %d entries, want one per chip", len(env.Temperatures))
	}
	if env.Temperatures[0].Source.Chip != vane.ChipHTS221 || env.Temperatures[1].Source.Chip != vane.ChipLPS25H {
		t.Errorf("temperatures are in the order %s, %s; want HTS221 then LPS25H",
			env.Temperatures[0].Source.Chip, env.Temperatures[1].Source.Chip)
	}
	if env.Temperatures[0].Err != nil || env.Temperatures[1].Err != nil {
		t.Errorf("temperature errors: %v, %v", env.Temperatures[0].Err, env.Temperatures[1].Err)
	}
	for _, s := range d.Sensors() {
		if s.Source.Bus != "/dev/i2c-1" || s.Err != nil {
			t.Errorf("sensor %+v", s)
		}
	}
}

// RF-05, the case the issue names: one chip failing leaves the other usable.
// Negative control, seen failing: making any one failure fail Open.
func TestOneChipFailingLeavesTheOtherUsable(t *testing.T) {
	b := newBoard(t)
	b.bus = vanetest.NewFakeBus(b.hts) // the LPS25H is not on the bus
	d := b.mustOpen()

	got := states(d)
	if got[vane.ChipHTS221] != sensehat.StateReady || got[vane.ChipLPS25H] != sensehat.StateFailed {
		t.Fatalf("states = %v", got)
	}
	for _, s := range d.Sensors() {
		if s.Source.Chip == vane.ChipLPS25H && !errors.Is(s.Err, vane.ErrNotPresent) {
			t.Errorf("the LPS25H's reason = %v, want ErrNotPresent", s.Err)
		}
	}

	env, err := d.Environment(bg)
	if err != nil {
		t.Fatalf("a partial failure must not be a top-level error: %v", err)
	}
	if env.Humidity.Err != nil || env.Humidity.Raw != 3500 {
		t.Errorf("the working chip's humidity = %+v", env.Humidity)
	}
	if !errors.Is(env.Pressure.Err, vane.ErrNotPresent) || env.Pressure.Source.Chip != vane.ChipLPS25H || env.Pressure.Raw != 0 {
		t.Errorf("the failed chip's pressure = %+v, want its error and its source, and no value", env.Pressure)
	}
	if len(env.Temperatures) != 2 || env.Temperatures[0].Err != nil || env.Temperatures[1].Err == nil {
		t.Errorf("temperatures = %+v, want the HTS221's valid and the LPS25H's failed", env.Temperatures)
	}
}

func TestARequiredChipThatFailsFailsOpen(t *testing.T) {
	b := newBoard(t)
	b.bus = vanetest.NewFakeBus(b.hts)
	_, err := b.open(sensehat.WithRequired(vane.ChipLPS25H))
	if err == nil || !errors.Is(err, vane.ErrNotPresent) {
		t.Fatalf("error = %v, want one wrapping ErrNotPresent for the required LPS25H", err)
	}
	// And the same board opens when that chip is not required.
	if _, err := b.open(sensehat.WithRequired(vane.ChipHTS221)); err != nil {
		t.Errorf("requiring the chip that works: %v", err)
	}
	// A failed Open leaves nothing powered up.
	if got := b.hts.Peek(0x20); got != 0x04 && got != 0x84 {
		t.Errorf("CTRL_REG1 = %#x", got)
	}
}

func TestAFailedOpenPowersDownWhatItOpened(t *testing.T) {
	b := newBoard(t)
	b.bus = vanetest.NewFakeBus(b.hts) // HTS221 opens, the required LPS25H cannot
	if _, err := b.open(sensehat.WithRequired(vane.ChipLPS25H)); err == nil {
		t.Fatal("Open succeeded without the required chip")
	}
	if got := b.hts.Peek(0x20); got != 0x04 {
		t.Errorf("the HTS221 opened before the failure was left with CTRL_REG1 = %#x, want 0x04: powered down, BDU on", got)
	}
}

func TestEveryChipFailingFailsOpenWithAllTheReasons(t *testing.T) {
	b := newBoard(t)
	b.bus = vanetest.NewFakeBus()
	_, err := b.open()
	if err == nil {
		t.Fatal("Open succeeded on a bus with nothing on it")
	}
	var se *vane.SensorError
	if !errors.Is(err, vane.ErrNotPresent) || !errors.As(err, &se) {
		t.Errorf("error = %v, want the underlying ErrNotPresent", err)
	}
	for _, chip := range []string{"HTS221", "LPS25H"} {
		if !contains(err.Error(), chip) {
			t.Errorf("the error does not mention %s: %v", chip, err)
		}
	}
}

// RF-07. The kernel owns the LPS25H on a standard install, and vane must not
// touch it. Negative control, seen failing: removing the ownership check makes
// both the state and the no-traffic assertions fail.
func TestAKernelOwnedChipIsReportedAndNeverTouched(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	d := b.mustOpen()

	if got := states(d); got[vane.ChipLPS25H] != sensehat.StateKernelOwned || got[vane.ChipHTS221] != sensehat.StateReady {
		t.Fatalf("states = %v", got)
	}
	for _, s := range d.Sensors() {
		if s.Source.Chip != vane.ChipLPS25H {
			continue
		}
		var ko *vane.KernelOwnedError
		if !errors.Is(s.Err, vane.ErrKernelOwned) || !errors.As(s.Err, &ko) || ko.Driver != "st-press-i2c" || ko.Source.Address != 0x5C {
			t.Errorf("the LPS25H's reason = %v, want a KernelOwnedError naming st-press-i2c", s.Err)
		}
	}
	if b.touched(0x5C) {
		t.Fatalf("a chip the kernel owns was addressed:\n%v", b.bus.Log())
	}
	env, _ := d.Environment(bg)
	if !errors.Is(env.Pressure.Err, vane.ErrKernelOwned) {
		t.Errorf("Pressure.Err = %v, want ErrKernelOwned", env.Pressure.Err)
	}
	if b.touched(0x5C) {
		t.Error("reading the environment addressed a chip the kernel owns")
	}
}

func TestAKernelOwnedRequiredChipFailsOpenWithTheDriverName(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	_, err := b.open(sensehat.WithRequired(vane.ChipLPS25H))
	var ko *vane.KernelOwnedError
	if !errors.Is(err, vane.ErrKernelOwned) || !errors.As(err, &ko) || ko.Driver != "st-press-i2c" {
		t.Fatalf("error = %v, want one that names the kernel driver", err)
	}
	if b.touched(0x5C) {
		t.Error("the owned chip was addressed")
	}
}

func TestEveryChipOwnedByTheKernelFailsOpen(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	b.bind(0x5F, "hts221")
	if _, err := b.open(); !errors.Is(err, vane.ErrKernelOwned) {
		t.Fatalf("error = %v, want ErrKernelOwned", err)
	}
	if len(b.bus.Log()) != 0 {
		t.Errorf("%d transactions on a board the kernel owns entirely", len(b.bus.Log()))
	}
}

func TestTheKernelCheckCanBeTurnedOff(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	d := b.mustOpen(sensehat.WithKernelCheck(false))
	if got := states(d); got[vane.ChipLPS25H] != sensehat.StateReady {
		t.Errorf("states = %v, want the LPS25H ready with the check off", got)
	}
}

// Skipping a safety check because it cannot run is failing open.
func TestABusThatHasNoSysfsNodeIsRefusedUnlessTheCheckIsOff(t *testing.T) {
	b := newBoard(t)
	if _, err := b.open(sensehat.WithBusPath("fake-bus")); err == nil {
		t.Error("a bus path the kernel check cannot use was accepted")
	}
	if len(b.bus.Log()) != 0 {
		t.Error("the bus was used before the refusal")
	}
	d := b.mustOpen(sensehat.WithBusPath("fake-bus"), sensehat.WithKernelCheck(false))
	if d.Sensors()[0].Source.Bus != "fake-bus" {
		t.Errorf("source bus = %q", d.Sensors()[0].Source.Bus)
	}
}

func TestAKernelCheckThatCannotAnswerFailsTheChipNotTheCheck(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	b := newBoard(t)
	dir := filepath.Join(b.root, "sys", "bus", "i2c", "devices", "1-005c")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver"), []byte("x"), 0o600); err != nil { // present and not a link
		t.Fatal(err)
	}
	d := b.mustOpen()
	if got := states(d); got[vane.ChipLPS25H] != sensehat.StateFailed {
		t.Errorf("states = %v, want the LPS25H failed: an unanswerable check is not \"not owned\"", got)
	}
	if b.touched(0x5C) {
		t.Error("a chip whose ownership could not be established was addressed")
	}
}

func TestOnlyTheRequestedChipsAreTouched(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen(sensehat.WithChips(vane.ChipHTS221))
	if got := states(d); got[vane.ChipHTS221] != sensehat.StateReady || got[vane.ChipLPS25H] != sensehat.StateNotRequested {
		t.Fatalf("states = %v", got)
	}
	env, err := d.Environment(bg)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(env.Pressure.Err, sensehat.ErrNotRequested) || env.Pressure.Source.Chip != vane.ChipLPS25H {
		t.Errorf("Pressure = %+v, want ErrNotRequested", env.Pressure)
	}
	if len(env.Temperatures) != 1 || env.Temperatures[0].Source.Chip != vane.ChipHTS221 {
		t.Errorf("Temperatures = %+v, want only the HTS221's", env.Temperatures)
	}
	if b.touched(0x5C) {
		t.Error("a chip that was not requested was addressed")
	}
}

func TestAConfigurationErrorIsRefusedBeforeTheBusIsUsed(t *testing.T) {
	for name, opt := range map[string]sensehat.Option{
		"no chips":                   sensehat.WithChips(),
		"an unknown chip":            sensehat.WithChips("BME280"),
		"a chip twice":               sensehat.WithChips(vane.ChipHTS221, vane.ChipHTS221),
		"a required chip not wanted": sensehat.WithRequired("BME280"),
		"a nil bus":                  sensehat.WithBus(nil),
		"an empty bus path":          sensehat.WithBusPath(""),
		"a nil clock":                sensehat.WithClock(nil),
		"a zero close timeout":       sensehat.WithCloseTimeout(0),
	} {
		b := newBoard(t)
		if _, err := b.open(opt); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if len(b.bus.Log()) != 0 {
			t.Errorf("%s: the bus was used before the option was refused", name)
		}
	}
	b := newBoard(t)
	if _, err := b.open(sensehat.WithChips(vane.ChipHTS221), sensehat.WithRequired(vane.ChipLPS25H)); err == nil {
		t.Error("requiring a chip that WithChips left out was accepted")
	}
}

// Each of these also fails later for another reason, with a worse message: no
// chips requested ends as "no sensor could be used", and an empty path as
// "cannot check kernel ownership". The test pins the reason that names the
// mistake, which is only given if the option is validated first.
func TestAConfigurationErrorSaysWhatIsWrong(t *testing.T) {
	for name, tc := range map[string]struct {
		opts []sensehat.Option
		want string
	}{
		"no chips":        {[]sensehat.Option{sensehat.WithChips()}, "no chip requested"},
		"an empty path":   {[]sensehat.Option{sensehat.WithBusPath(""), sensehat.WithKernelCheck(false)}, "empty bus path"},
		"an unknown chip": {[]sensehat.Option{sensehat.WithChips("BME280")}, "unknown chip"},
		"a zero timeout":  {[]sensehat.Option{sensehat.WithCloseTimeout(0)}, "close timeout"},
	} {
		b := newBoard(t)
		_, err := b.open(tc.opts...)
		if err == nil || !contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one saying %q", name, err, tc.want)
		}
		if len(b.bus.Log()) != 0 {
			t.Errorf("%s: the bus was used", name)
		}
	}
}

// The board's addresses are fixed and the kernel check needs them before the
// driver exists, so an address passed through a driver option must lose.
func TestADriverOptionCannotMoveAChipToAnotherAddress(t *testing.T) {
	b := newBoard(t)
	d := b.mustOpen(sensehat.WithHTS221(hts221.WithAddress(0x10)), sensehat.WithLPS25H(lps25h.WithAddress(0x11)))
	for _, s := range d.Sensors() {
		want := map[vane.Chip]uint16{vane.ChipHTS221: 0x5F, vane.ChipLPS25H: 0x5C}[s.Source.Chip]
		if s.Source.Address != want || s.State != sensehat.StateReady {
			t.Errorf("sensor %+v, want address %#x ready", s, want)
		}
	}
	if b.touched(0x10) || b.touched(0x11) {
		t.Error("a chip was addressed where the driver option said")
	}
}

func TestDriverOptionsReachTheDrivers(t *testing.T) {
	b := newBoard(t)
	b.mustOpen(sensehat.WithHTS221(hts221.WithMode(hts221.Continuous(hts221.Rate7Hz))),
		sensehat.WithLPS25H(lps25h.WithMode(lps25h.Continuous(lps25h.Rate25Hz))))
	if got := b.hts.Peek(0x20); got != 0x86 {
		t.Errorf("HTS221 CTRL_REG1 = %#x, want continuous 7 Hz (0x86)", got)
	}
	if got := b.lps.Peek(0x20); got != 0xC4 {
		t.Errorf("LPS25H CTRL_REG1 = %#x, want continuous 25 Hz (0xc4)", got)
	}
}

func TestBoardInfoComesFromTheEEPROMWhenThereIsOne(t *testing.T) {
	b := newBoard(t)
	if d := b.mustOpen(); d.Board().Present {
		t.Errorf("Board = %+v, want absent when the device tree has no HAT", d.Board())
	}
	dir := filepath.Join(b.root, "proc", "device-tree", "hat")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"product": "Raspberry Pi Sense HAT\x00", "vendor": "Raspberry Pi Foundation\x00", "product_ver": "0x0002\x00"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := b.mustOpen().Board()
	want := sensehat.BoardInfo{Present: true, Product: "Raspberry Pi Sense HAT", Vendor: "Raspberry Pi Foundation", ProductVer: "0x0002"}
	if got != want {
		t.Errorf("Board = %+v, want %+v", got, want)
	}
}

func TestSensorsReturnsACopy(t *testing.T) {
	d := newBoard(t).mustOpen()
	s := d.Sensors()
	s[0].State = sensehat.StateFailed
	if d.Sensors()[0].State != sensehat.StateReady {
		t.Error("modifying the result of Sensors changed the Device")
	}
}

func TestStateStrings(t *testing.T) {
	for s, want := range map[sensehat.State]string{
		sensehat.StateReady: "ready", sensehat.StateFailed: "failed",
		sensehat.StateKernelOwned: "kernel-owned", sensehat.StateNotRequested: "not-requested", 0: "State(0)",
	} {
		if got := s.String(); got != want {
			t.Errorf("State(%d) = %q, want %q", uint8(s), got, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// cancelAfterConfigure cancels a context right after the HTS221 is configured,
// so Open is interrupted between two chips.
type cancelAfterConfigure struct {
	vane.Bus
	cancel context.CancelFunc
}

func (c *cancelAfterConfigure) Tx(ctx context.Context, addr uint16, w, r []byte) error {
	err := c.Bus.Tx(ctx, addr, w, r)
	if addr == 0x5F && len(w) == 2 && w[0] == 0x20 { // CTRL_REG1: the chip is now powered up
		c.cancel()
	}
	return err
}

// The context that interrupts Open is the one that has ended, and closing what
// was opened still has to work. Negative control, seen failing: closing with the
// cancelled context itself leaves the HTS221 powered up.
func TestAnOpenCancelledHalfWayPowersDownWhatItOpened(t *testing.T) {
	b := newBoard(t)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	bus := &cancelAfterConfigure{Bus: b.bus, cancel: cancel}

	_, err := sensehat.Open(ctx, sensehat.WithBus(bus), sensehat.WithRoot(b.root), sensehat.WithClock(fixedClock))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := b.hts.Peek(0x20); got != 0x04 {
		t.Errorf("the HTS221 opened before the cancellation has CTRL_REG1 = %#x, want 0x04: powered down, BDU on", got)
	}
	if b.touched(0x5C) {
		t.Error("the second chip was addressed after the context had ended")
	}
}
