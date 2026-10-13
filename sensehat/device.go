package sensehat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/i2cdev"
	"github.com/JonasBorgesLM/vane/internal/kernel"
)

// State says what became of a chip when Open tried to use it.
type State uint8

// The states a chip can be in after Open.
const (
	// StateReady: the chip was identified and configured and can be read.
	StateReady State = iota + 1
	// StateFailed: the chip could not be opened; SensorInfo.Err says why.
	StateFailed
	// StateKernelOwned: a kernel driver is bound to the chip, so vane did not touch
	// it. SensorInfo.Err is a *vane.KernelOwnedError naming the driver.
	StateKernelOwned
	// StateNotRequested: the chip was left out by WithChips and was never touched.
	StateNotRequested
)

func (s State) String() string {
	switch s {
	case StateReady:
		return "ready"
	case StateFailed:
		return "failed"
	case StateKernelOwned:
		return "kernel-owned"
	case StateNotRequested:
		return "not-requested"
	default:
		return fmt.Sprintf("State(%d)", uint8(s))
	}
}

// SensorInfo is what Open found out about one chip. It describes the outcome of
// Open and does not change afterwards: a read that fails later is that read's
// error, not a new state.
type SensorInfo struct {
	Source vane.Source
	State  State
	Err    error // why the chip is not ready; nil when it is, or was not requested
}

// BoardInfo is the Sense HAT's identity as its EEPROM states it, when the
// firmware exposed it. It informs; it is never used to decide whether a chip is
// there (ADR-0003).
type BoardInfo struct {
	Present                     bool
	Product, Vendor, ProductVer string
}

type slot struct {
	info SensorInfo
	a    adapter
}

// Device is a Sense HAT with its environmental sensors opened.
type Device struct {
	slots        []slot
	board        BoardInfo
	closeTimeout time.Duration
	ownedBus     io.Closer // the bus Open opened, if it opened one

	mu     sync.Mutex
	closed bool
}

// Open finds the sensors on the board and opens the ones it can.
//
// For each requested chip it first asks the kernel whether a driver already owns
// it (unless WithKernelCheck(false)), and if one does it does not touch the chip
// (RF-07); then it opens the driver, which identifies the chip before writing.
// A chip that cannot be used is reported by Sensors, and Open still succeeds
// with the others, unless WithRequired names it or nothing at all could be
// opened (ADR-0006). If Open opened the bus itself, Close closes it.
func Open(ctx context.Context, opts ...Option) (*Device, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	busNum, haveNum := kernel.BusNumber(cfg.busPath)
	if cfg.kernelCheck && !haveNum {
		return nil, fmt.Errorf("sensehat: cannot check kernel ownership on %q, which is not /dev/i2c-N; "+
			"use WithKernelCheck(false) if it is not a kernel adapter", cfg.busPath)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d := &Device{closeTimeout: cfg.closeTimeout}
	bus := cfg.bus
	if !cfg.busSet {
		b, err := i2cdev.Open(cfg.busPath)
		if err != nil {
			return nil, err
		}
		bus, d.ownedBus = b, b
	}
	if product, vendor, ver, ok := kernel.Board(cfg.root); ok {
		d.board = BoardInfo{Present: true, Product: product, Vendor: vendor, ProductVer: ver}
	}

	for _, p := range probeTable {
		src := vane.Source{Chip: p.chip, Bus: cfg.busPath, Address: p.addr}
		sl := slot{info: SensorInfo{Source: src}, a: p.new(src)}
		if cfg.wants(p.chip) {
			sl.info.State, sl.info.Err = cfg.bring(ctx, bus, busNum, sl.a, src)
		} else {
			sl.info.State = StateNotRequested
		}
		d.slots = append(d.slots, sl)
		if err := ctx.Err(); err != nil {
			return nil, d.abandon(ctx, err)
		}
	}

	var unmet []error
	for _, sl := range d.slots {
		if slices.Contains(cfg.required, sl.info.Source.Chip) && sl.info.State != StateReady {
			unmet = append(unmet, fmt.Errorf("sensehat: required %s is %s: %w", sl.info.Source.Chip, sl.info.State, sl.info.Err))
		}
	}
	if len(unmet) > 0 {
		return nil, d.abandon(ctx, errors.Join(unmet...))
	}
	if !d.anyReady() {
		var why []error
		for _, sl := range d.slots {
			if sl.info.Err != nil {
				why = append(why, sl.info.Err)
			}
		}
		return nil, d.abandon(ctx, fmt.Errorf("sensehat: no sensor could be used: %w", errors.Join(why...)))
	}
	return d, nil
}

// bring takes one requested chip from "on the board" to ready, or says why not.
// Whatever it cannot find out counts against the chip: an ownership check that
// fails is not "not owned", because that would let vane write to a chip it
// could not see.
func (c *config) bring(ctx context.Context, bus vane.Bus, busNum int, a adapter, src vane.Source) (State, error) {
	if c.kernelCheck {
		driver, owned, err := kernel.Owner(c.root, busNum, src.Address)
		if err != nil {
			return StateFailed, &vane.SensorError{Source: src, Op: "identify", Err: err}
		}
		if owned {
			return StateKernelOwned, &vane.KernelOwnedError{Source: src, Driver: driver}
		}
	}
	if err := a.open(ctx, bus, c); err != nil {
		return StateFailed, err
	}
	return StateReady, nil
}

func (d *Device) anyReady() bool {
	return slices.ContainsFunc(d.slots, func(s slot) bool { return s.info.State == StateReady })
}

// abandon closes what Open had opened when it is about to fail, and returns the
// failure with anything the closing added. The context that made Open fail may
// be the one that has ended, and closing still has to happen, so the closing
// keeps its values and ignores its cancellation.
func (d *Device) abandon(ctx context.Context, err error) error {
	if cerr := d.closeAll(context.WithoutCancel(ctx)); cerr != nil {
		return errors.Join(err, cerr)
	}
	return err
}

// Sensors reports what Open found out about each chip on the board, in a fixed
// order. The result is a copy.
func (d *Device) Sensors() []SensorInfo {
	out := make([]SensorInfo, len(d.slots))
	for i, s := range d.slots {
		out[i] = s.info
	}
	return out
}

// Board returns the HAT's identity from its EEPROM, or a zero BoardInfo when the
// firmware did not expose it.
func (d *Device) Board() BoardInfo { return d.board }

// Close powers every opened chip down and, if Open opened the bus, closes it. It
// is idempotent: the first call returns the errors, later ones return nil.
//
// WithCloseTimeout bounds the transaction that powers each chip down, per chip,
// so Close can take as long as the number of chips times that. A Read in
// progress is waited for, and that wait is bounded by the driver's ready timeout.
func (d *Device) Close() error { return d.closeAll(context.Background()) }

// closeAll is Close with the context its per-chip deadlines are derived from.
func (d *Device) closeAll(parent context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	var errs []error
	for _, s := range d.slots {
		if s.info.State != StateReady {
			continue
		}
		// One deadline per chip. A single shared one would let a slow chip use up the
		// whole budget and leave every chip after it powered up.
		ctx, cancel := context.WithTimeout(parent, d.closeTimeout)
		err := s.a.close(ctx)
		cancel()
		if err != nil {
			errs = append(errs, err)
		}
	}
	if d.ownedBus != nil {
		if err := d.ownedBus.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *Device) isClosed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}
