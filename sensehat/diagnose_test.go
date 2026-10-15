package sensehat_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/sensehat"
	"github.com/JonasBorgesLM/vane/vanetest"
)

func (b *board) diagnose(opts ...sensehat.Option) (sensehat.Report, error) {
	b.t.Helper()
	all := make([]sensehat.Option, 0, 2+len(opts))
	all = append(all, sensehat.WithBus(b.bus), sensehat.WithRoot(b.root))
	return sensehat.Diagnose(bg, append(all, opts...)...)
}

func findings(r sensehat.Report) map[vane.Chip]sensehat.Finding {
	out := map[vane.Chip]sensehat.Finding{}
	for _, c := range r.Chips {
		out[c.Source.Chip] = c.Finding
	}
	return out
}

// A diagnostic must leave the board as it found it. Negative control, seen
// failing: a Diagnose that opens the drivers, which configures the chips.
func TestDiagnoseIdentifiesTheChipsAndWritesNothing(t *testing.T) {
	b := newBoard(t)
	rep, err := b.diagnose()
	if err != nil {
		t.Fatal(err)
	}
	got := findings(rep)
	if got[vane.ChipHTS221] != sensehat.FindingIdentified || got[vane.ChipLPS25H] != sensehat.FindingIdentified {
		t.Fatalf("findings = %v, want both identified", got)
	}
	for _, c := range rep.Chips {
		if !c.Usable() || c.Err != nil || c.Source.Bus != "/dev/i2c-1" {
			t.Errorf("chip %+v", c)
		}
	}
	for _, tx := range b.bus.Log() {
		if len(tx.R) == 0 {
			t.Errorf("Diagnose wrote to the board: %x at %#x", tx.W, tx.Addr)
		}
	}
	if len(b.bus.Log()) != 2 {
		t.Errorf("%d transactions, want one identity read per chip", len(b.bus.Log()))
	}
	for name, got := range map[string]byte{"HTS221": b.hts.Peek(0x20), "LPS25H": b.lps.Peek(0x20)} {
		if got != 0 {
			t.Errorf("%s CTRL_REG1 = %#x: the chip was powered up by a diagnostic", name, got)
		}
	}
}

func TestDiagnoseTellsAbsentFromWrongFromUnreachable(t *testing.T) {
	rep := func(mut func(*board)) sensehat.Report {
		b := newBoard(t)
		mut(b)
		r, err := b.diagnose()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	absent := rep(func(b *board) { b.bus = vanetest.NewFakeBus(b.hts) })
	if findings(absent)[vane.ChipLPS25H] != sensehat.FindingAbsent || !errors.Is(absent.Chips[1].Err, vane.ErrNotPresent) {
		t.Errorf("a chip not on the bus: %+v", absent.Chips[1])
	}
	wrong := rep(func(b *board) {
		b.lps = vanetest.NewLPS25H(vanetest.WithWhoAmI(0xBC), vanetest.WithReporter(&reporter{t}))
		b.bus = vanetest.NewFakeBus(b.hts, b.lps)
	})
	if findings(wrong)[vane.ChipLPS25H] != sensehat.FindingWrongChip || !errors.Is(wrong.Chips[1].Err, vane.ErrUnexpectedChip) {
		t.Errorf("a chip answering as another: %+v", wrong.Chips[1])
	}
	broken := rep(func(b *board) { b.bus.Inject(0x5C, vanetest.EIO()) })
	if findings(broken)[vane.ChipLPS25H] != sensehat.FindingUnreachable || !errors.Is(broken.Chips[1].Err, vanetest.ErrIO) {
		t.Errorf("a bus error: %+v", broken.Chips[1])
	}
	if findings(broken)[vane.ChipHTS221] != sensehat.FindingIdentified {
		t.Error("one chip's bus error affected the other's finding")
	}
}

func TestDiagnoseDoesNotTouchAChipTheKernelOwns(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	rep, err := b.diagnose()
	if err != nil {
		t.Fatal(err)
	}
	var lps sensehat.ChipReport
	for _, c := range rep.Chips {
		if c.Source.Chip == vane.ChipLPS25H {
			lps = c
		}
	}
	if lps.Finding != sensehat.FindingKernelOwned || lps.Driver != "st-press-i2c" || lps.Usable() || !errors.Is(lps.Err, vane.ErrKernelOwned) {
		t.Errorf("the LPS25H = %+v, want kernel-owned by st-press-i2c, not usable", lps)
	}
	if b.touched(0x5C) {
		t.Error("a chip the kernel owns was addressed")
	}
	if findings(rep)[vane.ChipHTS221] != sensehat.FindingIdentified {
		t.Error("the other chip should still be identified")
	}
}

func TestDiagnoseWithABusThatWillNotOpenStillSaysWhatTheKernelKnows(t *testing.T) {
	b := newBoard(t)
	b.bindOn(251, 0x5C, "st-press-i2c")
	// No WithBus: Diagnose opens the path itself. Bus 251 is far from any adapter a
	// machine has, so this never opens a real one, which on a Pi it would.
	rep, err := sensehat.Diagnose(bg, sensehat.WithBusPath("/dev/i2c-251"), sensehat.WithRoot(b.root))
	if err != nil {
		t.Fatalf("a bus that does not open is a finding, not an error: %v", err)
	}
	if rep.BusErr == nil {
		t.Fatal("BusErr is nil for a bus that cannot be opened")
	}
	got := findings(rep)
	if got[vane.ChipLPS25H] != sensehat.FindingKernelOwned {
		t.Errorf("LPS25H = %v, want kernel-owned: that needs no bus", got[vane.ChipLPS25H])
	}
	if got[vane.ChipHTS221] != sensehat.FindingUnreachable {
		t.Errorf("HTS221 = %v, want unreachable", got[vane.ChipHTS221])
	}
	for _, c := range rep.Chips {
		if c.Finding == sensehat.FindingUnreachable && !errors.Is(c.Err, rep.BusErr) {
			t.Errorf("an unreachable chip should carry the bus error: %v", c.Err)
		}
	}
}

func TestDiagnoseOnlyLooksAtTheRequestedChips(t *testing.T) {
	b := newBoard(t)
	rep, err := b.diagnose(sensehat.WithChips(vane.ChipHTS221))
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep); got[vane.ChipLPS25H] != sensehat.FindingNotRequested || got[vane.ChipHTS221] != sensehat.FindingIdentified {
		t.Errorf("findings = %v", got)
	}
	if b.touched(0x5C) {
		t.Error("a chip that was not requested was addressed")
	}
}

func TestMissingNamesTheRequiredChipsThatAreNotUsable(t *testing.T) {
	b := newBoard(t)
	b.bind(0x5C, "st-press-i2c")
	rep, _ := b.diagnose(sensehat.WithRequired(vane.ChipHTS221, vane.ChipLPS25H))
	missing := rep.Missing()
	if len(missing) != 1 || missing[0] != vane.ChipLPS25H {
		t.Errorf("Missing() = %v, want only the kernel-owned LPS25H", missing)
	}
	rep, _ = newBoard(t).diagnose(sensehat.WithRequired(vane.ChipHTS221))
	if len(rep.Missing()) != 0 {
		t.Errorf("Missing() = %v, want none", rep.Missing())
	}
	rep, _ = newBoard(t).diagnose()
	if len(rep.Missing()) != 0 {
		t.Error("nothing was required, so nothing can be missing")
	}
}

func TestDiagnoseListsTheBusesAndTheBoard(t *testing.T) {
	b := newBoard(t)
	dir := filepath.Join(b.root, "sys", "class", "i2c-dev", "i2c-1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "name"), []byte("bcm2835 (i2c@7e804000)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hat := filepath.Join(b.root, "proc", "device-tree", "hat")
	if err := os.MkdirAll(hat, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hat, "product"), []byte("Raspberry Pi Sense HAT\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := b.diagnose()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Buses) != 1 || rep.Buses[0].Path != "/dev/i2c-1" || rep.Buses[0].Name != "bcm2835 (i2c@7e804000)" {
		t.Errorf("Buses = %+v", rep.Buses)
	}
	if !rep.Board.Present || rep.Board.Product != "Raspberry Pi Sense HAT" || rep.Bus != "/dev/i2c-1" {
		t.Errorf("Board = %+v, Bus = %q", rep.Board, rep.Bus)
	}
}

func TestDiagnoseRefusesAMisconfigurationAndAnEndedContext(t *testing.T) {
	b := newBoard(t)
	if _, err := b.diagnose(sensehat.WithChips()); err == nil {
		t.Error("no chips requested was accepted")
	}
	if _, err := b.diagnose(sensehat.WithBusPath("fake-bus")); err == nil {
		t.Error("a bus the kernel check cannot use was accepted")
	}
	cctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := sensehat.Diagnose(cctx, sensehat.WithBus(b.bus), sensehat.WithRoot(b.root)); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if len(b.bus.Log()) != 0 {
		t.Error("the bus was used by a refused call")
	}
}

func TestDiagnoseLeavesTheCallersBusOpen(t *testing.T) {
	b := newBoard(t)
	owned := &closeCounter{Bus: b.bus}
	if _, err := sensehat.Diagnose(bg, sensehat.WithBus(owned), sensehat.WithRoot(b.root)); err != nil {
		t.Fatal(err)
	}
	if owned.closes != 0 {
		t.Errorf("Diagnose closed the caller's bus %d times", owned.closes)
	}
}

func TestFindingStrings(t *testing.T) {
	for f, want := range map[sensehat.Finding]string{
		sensehat.FindingIdentified: "identified", sensehat.FindingKernelOwned: "kernel-owned", sensehat.FindingAbsent: "absent",
		sensehat.FindingWrongChip: "wrong-chip", sensehat.FindingUnreachable: "unreachable", sensehat.FindingNotRequested: "not-requested",
		0: "Finding(0)",
	} {
		if got := f.String(); got != want {
			t.Errorf("Finding(%d) = %q, want %q", uint8(f), got, want)
		}
	}
}
