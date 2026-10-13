package sensehat

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/i2cdev"
	"github.com/JonasBorgesLM/vane/internal/kernel"
)

// Finding is what Diagnose found out about one chip.
type Finding uint8

// The findings.
const (
	// FindingIdentified: the chip answered with the identity it should have.
	FindingIdentified Finding = iota + 1
	// FindingKernelOwned: a kernel driver is bound to it, so it was not touched.
	FindingKernelOwned
	// FindingAbsent: nothing acknowledged the address.
	FindingAbsent
	// FindingWrongChip: something answered, but not with the expected identity.
	FindingWrongChip
	// FindingUnreachable: the bus failed, or could not be opened.
	FindingUnreachable
	// FindingNotRequested: the chip was left out by WithChips and not touched.
	FindingNotRequested
)

func (f Finding) String() string {
	switch f {
	case FindingIdentified:
		return "identified"
	case FindingKernelOwned:
		return "kernel-owned"
	case FindingAbsent:
		return "absent"
	case FindingWrongChip:
		return "wrong-chip"
	case FindingUnreachable:
		return "unreachable"
	case FindingNotRequested:
		return "not-requested"
	default:
		return fmt.Sprintf("Finding(%d)", uint8(f))
	}
}

// BusInfo is an I2C adapter the kernel lists.
type BusInfo struct {
	Path   string // /dev/i2c-1
	Number int
	Name   string // the adapter's own name, if sysfs gives one
}

// ChipReport is what Diagnose found out about one chip.
type ChipReport struct {
	Source  vane.Source
	Finding Finding
	// Driver names the kernel driver when Finding is FindingKernelOwned.
	Driver string
	// Err is the detail behind a finding other than identified: the identity
	// error, the missing acknowledgement, the bus error.
	Err error
}

// Usable reports whether vane could drive the chip: it answered as it should and
// no kernel driver has it.
func (c ChipReport) Usable() bool { return c.Finding == FindingIdentified }

// Report is everything Diagnose gathered, for a person or a tool to read.
type Report struct {
	// Bus is the adapter that was examined.
	Bus string
	// BusErr is why the bus could not be opened, if it could not. The kernel
	// findings do not need the bus and are still filled in.
	BusErr error
	// Buses lists the adapters the kernel knows of.
	Buses []BusInfo
	Board BoardInfo
	Chips []ChipReport
	// Required is what WithRequired named.
	Required []vane.Chip
}

// Missing returns the required chips that vane could not use.
func (r Report) Missing() []vane.Chip {
	var out []vane.Chip
	for _, want := range r.Required {
		if !slices.ContainsFunc(r.Chips, func(c ChipReport) bool { return c.Source.Chip == want && c.Usable() }) {
			out = append(out, want)
		}
	}
	return out
}

// Diagnose looks at the board and reports what it sees, for vane-probe. It takes
// the same options as Open, and it does less: it never writes to a chip, never
// configures one, never powers one up. For each chip it asks the kernel who owns
// it and, if nobody does, reads its identity register and nothing else.
//
// Problems are findings in the Report and not errors: a bus that will not open, a
// chip that is not there and a chip the kernel owns are exactly what a diagnostic
// is for. The returned error is for a configuration that makes no sense or a
// context that ended.
func Diagnose(ctx context.Context, opts ...Option) (rep Report, err error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if verr := cfg.validate(); verr != nil {
		return Report{}, verr
	}
	busNum, haveNum := kernel.BusNumber(cfg.busPath)
	if cfg.kernelCheck && !haveNum {
		return Report{}, fmt.Errorf("sensehat: cannot check kernel ownership on %q, which is not /dev/i2c-N; "+
			"use WithKernelCheck(false) if it is not a kernel adapter", cfg.busPath)
	}
	if cerr := ctx.Err(); cerr != nil {
		return Report{}, cerr
	}

	rep = Report{Bus: cfg.busPath, Required: slices.Clone(cfg.required)}
	if buses, berr := kernel.Buses(cfg.root); berr == nil {
		for _, b := range buses {
			rep.Buses = append(rep.Buses, BusInfo{Path: b.Path, Number: b.Number, Name: b.Name})
		}
	}
	if product, vendor, ver, ok := kernel.Board(cfg.root); ok {
		rep.Board = BoardInfo{Present: true, Product: product, Vendor: vendor, ProductVer: ver}
	}

	bus := cfg.bus
	if !cfg.busSet {
		b, oerr := i2cdev.Open(cfg.busPath)
		if oerr != nil {
			rep.BusErr = oerr
		} else {
			bus = b
			defer func() {
				if cerr := b.Close(); cerr != nil && err == nil {
					err = cerr
				}
			}()
		}
	}

	for _, p := range probeTable {
		src := vane.Source{Chip: p.chip, Bus: cfg.busPath, Address: p.addr}
		rep.Chips = append(rep.Chips, cfg.examine(ctx, bus, busNum, p.new(src), src, rep.BusErr))
		if cerr := ctx.Err(); cerr != nil {
			return rep, cerr
		}
	}
	return rep, nil
}

// examine finds out what it can about one chip without changing it.
func (c *config) examine(ctx context.Context, bus vane.Bus, busNum int, a adapter, src vane.Source, busErr error) ChipReport {
	r := ChipReport{Source: src}
	if !c.wants(src.Chip) {
		r.Finding = FindingNotRequested
		return r
	}
	if c.kernelCheck {
		driver, owned, err := kernel.Owner(c.root, busNum, src.Address)
		if err != nil {
			r.Finding, r.Err = FindingUnreachable, &vane.SensorError{Source: src, Op: "identify", Err: err}
			return r
		}
		if owned {
			r.Finding, r.Driver = FindingKernelOwned, driver
			r.Err = &vane.KernelOwnedError{Source: src, Driver: driver}
			return r
		}
	}
	if busErr != nil {
		r.Finding, r.Err = FindingUnreachable, busErr
		return r
	}
	err := a.identify(ctx, bus, c)
	switch {
	case err == nil:
		r.Finding = FindingIdentified
	case errors.Is(err, vane.ErrUnexpectedChip):
		r.Finding, r.Err = FindingWrongChip, err
	case errors.Is(err, vane.ErrNotPresent):
		r.Finding, r.Err = FindingAbsent, err
	default:
		r.Finding, r.Err = FindingUnreachable, err
	}
	return r
}
