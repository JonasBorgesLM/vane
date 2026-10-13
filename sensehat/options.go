package sensehat

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/chip/lps25h"
)

// Option configures Open.
type Option func(*config)

const (
	defaultBusPath      = "/dev/i2c-1"
	defaultCloseTimeout = 2 * time.Second
)

type config struct {
	bus          vane.Bus
	busSet       bool
	busPath      string
	chips        []vane.Chip
	chipsSet     bool
	required     []vane.Chip
	hts          []hts221.Option
	lps          []lps25h.Option
	now          func() time.Time
	kernelCheck  bool
	root         string
	closeTimeout time.Duration
}

func defaultConfig() config {
	return config{
		busPath:      defaultBusPath,
		now:          time.Now,
		kernelCheck:  true,
		root:         "/",
		closeTimeout: defaultCloseTimeout,
	}
}

// validate rejects a configuration before any bus is opened or touched.
func (c *config) validate() error {
	if c.busSet && c.bus == nil {
		return errors.New("sensehat: WithBus was given a nil bus")
	}
	if c.busPath == "" {
		return errors.New("sensehat: an empty bus path")
	}
	if c.now == nil {
		return errors.New("sensehat: nil clock")
	}
	if c.closeTimeout <= 0 {
		return errors.New("sensehat: the close timeout must be positive")
	}
	wanted := c.requested()
	if len(wanted) == 0 {
		return errors.New("sensehat: no chip requested")
	}
	for i, chip := range wanted {
		if !knownChip(chip) {
			return fmt.Errorf("sensehat: unknown chip %q", chip)
		}
		if slices.Contains(wanted[:i], chip) {
			return fmt.Errorf("sensehat: chip %s requested twice", chip)
		}
	}
	for _, chip := range c.required {
		if !slices.Contains(wanted, chip) {
			return fmt.Errorf("sensehat: %s is required but not among the requested chips", chip)
		}
	}
	return nil
}

// requested returns the chips to probe: those named by WithChips, or all of them.
func (c *config) requested() []vane.Chip {
	if c.chipsSet {
		return c.chips
	}
	all := make([]vane.Chip, len(probeTable))
	for i, p := range probeTable {
		all[i] = p.chip
	}
	return all
}

func (c *config) wants(chip vane.Chip) bool { return slices.Contains(c.requested(), chip) }

// WithBus makes Open use b instead of opening the device at the bus path. The
// caller keeps ownership: Close does not close b. The bus path still names the
// adapter for the kernel check and for each measurement's source.
func WithBus(b vane.Bus) Option { return func(c *config) { c.bus, c.busSet = b, true } }

// WithBusPath sets the adapter device, which is opened unless WithBus is also
// given. The default is "/dev/i2c-1", the Pi's header; confirm it with
// vane-probe. A path of the form /dev/i2c-N is what lets the kernel check find
// the adapter in sysfs.
func WithBusPath(path string) Option { return func(c *config) { c.busPath = path } }

// WithChips restricts Open to the named chips. The others are reported as not
// requested and are never touched. The default is every chip on the board.
func WithChips(chips ...vane.Chip) Option {
	return func(c *config) { c.chips, c.chipsSet = slices.Clone(chips), true }
}

// WithRequired makes Open fail unless the named chips could all be opened. They
// must be among the requested chips.
func WithRequired(chips ...vane.Chip) Option {
	return func(c *config) { c.required = slices.Clone(chips) }
}

// WithHTS221 passes options to the HTS221 driver, such as its mode or poll
// timing. WithAddress has no effect through it: the board's addresses are fixed,
// and the kernel check has to know the address before the driver is built.
func WithHTS221(opts ...hts221.Option) Option {
	return func(c *config) { c.hts = append(c.hts, opts...) }
}

// WithLPS25H passes options to the LPS25H driver. WithAddress has no effect
// through it, for the reason given at WithHTS221.
func WithLPS25H(opts ...lps25h.Option) Option {
	return func(c *config) { c.lps = append(c.lps, opts...) }
}

// WithClock sets the clock that stamps every measurement. The default is
// time.Now.
func WithClock(now func() time.Time) Option { return func(c *config) { c.now = now } }

// WithKernelCheck turns the check for kernel-driver ownership on or off. It is
// on by default, and a bus path that is not /dev/i2c-N makes Open fail with it
// on, since there is then no sysfs node to look at and skipping the check
// silently would be failing open. Turn it off for a bus that is not a kernel
// adapter, and in tests.
func WithKernelCheck(enabled bool) Option { return func(c *config) { c.kernelCheck = enabled } }

// WithRoot sets the directory under which sysfs and the device tree are read.
// The default is "/". It exists for tests, which lay out a fake sysfs.
func WithRoot(root string) Option { return func(c *config) { c.root = root } }

// WithCloseTimeout bounds the transaction that powers each chip down in Close,
// separately for each chip. The default is two seconds. It does not bound the
// wait for a Read already in progress; that is bounded by the driver's ready
// timeout.
func WithCloseTimeout(d time.Duration) Option { return func(c *config) { c.closeTimeout = d } }

func knownChip(chip vane.Chip) bool {
	for _, p := range probeTable {
		if p.chip == chip {
			return true
		}
	}
	return false
}
