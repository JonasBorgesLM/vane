// Command vane-probe looks at a Raspberry Pi Sense HAT and says what vane can do
// with it. It replaces the manual check with i2c-tools: it lists the I2C
// adapters, says for each chip whether a kernel driver has it, and for those
// that are free reads the identity register and compares it with the expected
// one.
//
// It only looks. It never configures a chip, never powers one up and never
// writes to the bus, so it is safe to run on a board something else is using.
//
//	vane-probe [-bus /dev/i2c-1] [-require hts221,lps25h|all]
//
// The exit status is 0 when nothing prevents vane from working, 1 when the bus
// cannot be opened or a required chip is not usable, and 2 for a bad command
// line. A chip the kernel owns is reported but is only a failure if it is
// required.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/sensehat"
)

// version is set by the release build with -ldflags "-X main.version=...".
var version = "dev"

// env is what a test substitutes for the machine.
type env struct {
	bus vane.Bus // nil means open the adapter named by -bus
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, env{}))
}

// complain writes a message to stderr. If stderr itself cannot be written there
// is nowhere left to say so, which is why the error is not kept.
func complain(stderr io.Writer, format string, args ...any) {
	(&printer{w: stderr}).printf(format, args...)
}

func run(args []string, stdout, stderr io.Writer, e env) int {
	fs := flag.NewFlagSet("vane-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bus := fs.String("bus", "/dev/i2c-1", "the I2C adapter to examine")
	require := fs.String("require", "", "chips that must be usable, or the exit status is 1: hts221, lps25h, or all")
	root := fs.String("root", "/", "the directory sysfs and the device tree are read under; for tests and containers")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		p := &printer{w: stdout}
		p.printf("vane-probe %s\n", version)
		if p.err != nil {
			complain(stderr, "vane-probe: %v\n", p.err)
			return 1
		}
		return 0
	}
	required, err := parseRequired(*require)
	if err != nil {
		complain(stderr, "vane-probe: %v\n", err)
		return 2
	}

	opts := []sensehat.Option{sensehat.WithBusPath(*bus), sensehat.WithRoot(*root), sensehat.WithRequired(required...)}
	if e.bus != nil {
		opts = append(opts, sensehat.WithBus(e.bus))
	}
	rep, err := sensehat.Diagnose(context.Background(), opts...)
	if err != nil {
		complain(stderr, "vane-probe: %v\n", err)
		return 2
	}
	missing, werr := render(stdout, rep)
	if werr != nil {
		complain(stderr, "vane-probe: writing the report: %v\n", werr)
		return 1
	}
	if rep.BusErr != nil || len(missing) > 0 {
		return 1
	}
	return 0
}

// parseRequired reads the -require list.
func parseRequired(list string) ([]vane.Chip, error) {
	var out []vane.Chip
	for _, name := range strings.Split(list, ",") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "":
		case "all":
			out = append(out, vane.ChipHTS221, vane.ChipLPS25H)
		case "hts221":
			out = append(out, vane.ChipHTS221)
		case "lps25h", "lps25hb":
			out = append(out, vane.ChipLPS25H)
		default:
			return nil, fmt.Errorf("unknown chip %q in -require: use hts221, lps25h or all", name)
		}
	}
	return dedupe(out), nil
}

func dedupe(chips []vane.Chip) []vane.Chip {
	var out []vane.Chip
	for _, c := range chips {
		if !contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func contains(chips []vane.Chip, c vane.Chip) bool {
	for _, x := range chips {
		if x == c {
			return true
		}
	}
	return false
}

// printer writes text and remembers the first error, so a report is not left
// half-written without anyone knowing: a closed pipe is a failure of the tool.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}

func (p *printer) println(s string) { p.printf("%s\n", s) }

// render writes the report and returns the required chips that are not usable,
// and the first error writing it.
func render(w io.Writer, rep sensehat.Report) ([]vane.Chip, error) {
	p := &printer{w: w}
	p.printf("vane-probe %s\n\n", version)

	p.println("I2C adapters")
	if len(rep.Buses) == 0 {
		p.println("  none listed by the kernel (is the I2C interface enabled? raspi-config, Interface Options)")
	}
	for _, b := range rep.Buses {
		mark := ""
		if b.Path == rep.Bus {
			mark = "   <- examined"
		}
		p.printf("  %-12s %s%s\n", b.Path, b.Name, mark)
	}

	p.println("\nHAT")
	if rep.Board.Present {
		p.printf("  %s, %s, version %s\n", orUnknown(rep.Board.Product), orUnknown(rep.Board.Vendor), orUnknown(rep.Board.ProductVer))
	} else {
		p.println("  not reported by the firmware. That does not mean there is no HAT: the EEPROM may be\n  absent or unread. The chips below are what counts.")
	}

	p.printf("\nChips on %s\n", rep.Bus)
	if rep.BusErr != nil {
		p.printf("  cannot open %s: %v\n", rep.Bus, rep.BusErr)
	}
	usable := 0
	for _, c := range rep.Chips {
		if c.Usable() {
			usable++
		}
		sharesBusErr := rep.BusErr != nil && errors.Is(c.Err, rep.BusErr)
		p.printf("  %-7s 0x%02x   %s\n", c.Source.Chip, c.Source.Address, describe(c, sharesBusErr))
		if advice := adviceFor(c, sharesBusErr); advice != "" {
			p.printf("          %s\n", advice)
		}
	}

	missing := rep.Missing()
	p.printf("\n%d of %d chips usable by vane.", usable, len(rep.Chips))
	if len(missing) > 0 {
		names := make([]string, len(missing))
		for i, m := range missing {
			names[i] = string(m)
		}
		p.printf(" Required but not usable: %s.", strings.Join(names, ", "))
	}
	p.println("")
	return missing, p.err
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// describe says what was found. A chip whose only problem is that the bus would
// not open says so briefly and points at the error already printed once.
func describe(c sensehat.ChipReport, sharesBusErr bool) string {
	if sharesBusErr {
		return "unreachable: the bus could not be opened (see above)"
	}
	switch c.Finding {
	case sensehat.FindingIdentified:
		return "identified"
	case sensehat.FindingKernelOwned:
		return fmt.Sprintf("owned by the kernel driver %q", c.Driver)
	case sensehat.FindingAbsent:
		return "absent: nothing acknowledged the address"
	case sensehat.FindingWrongChip:
		return fmt.Sprintf("a different chip answered: %v", c.Err)
	case sensehat.FindingUnreachable:
		return fmt.Sprintf("unreachable: %v", c.Err)
	case sensehat.FindingNotRequested:
		return "not examined"
	default:
		return c.Finding.String()
	}
}

// adviceFor says what to do, and only what is known. How to hand a kernel-owned
// chip to vane is the open question of ADR-0001, so the advice for it points at
// the decision and does not invent a procedure.
func adviceFor(c sensehat.ChipReport, sharesBusErr bool) string {
	if sharesBusErr {
		return ""
	}
	switch c.Finding {
	case sensehat.FindingKernelOwned:
		return "vane does not touch a chip the kernel drives. How to hand it over on a Pi is still\n" +
			"          being decided (ADR-0001, docs/hardware.md); until then, read it through the kernel."
	case sensehat.FindingAbsent:
		return "check that the HAT is seated on the header, and that the I2C interface is enabled."
	case sensehat.FindingWrongChip:
		return "an address answered with another chip's identity; this is not the board vane expects."
	case sensehat.FindingUnreachable:
		return "the bus failed; see the error above."
	case sensehat.FindingIdentified, sensehat.FindingNotRequested:
		return ""
	default:
		return ""
	}
}
