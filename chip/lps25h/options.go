package lps25h

import (
	"errors"
	"fmt"
	"time"
)

// Option configures a Sensor.
type Option func(*config)

type config struct {
	addr         uint16
	bus          string
	now          func() time.Time
	pollInterval time.Duration
	readyTimeout time.Duration
	mode         Mode
}

// Defaults for waiting on a one-shot conversion. The timeout is a deliberate
// over-estimate: the datasheet's conversion times are not confirmed yet (see
// docs/hardware.md), and it only decides how long a failure takes to report.
const (
	defaultPollInterval = 5 * time.Millisecond
	defaultReadyTimeout = time.Second
)

func defaultConfig() config {
	return config{
		addr:         DefaultAddress,
		now:          time.Now,
		pollInterval: defaultPollInterval,
		readyTimeout: defaultReadyTimeout,
	}
}

func (c *config) validate() error {
	if c.addr > 0x7F {
		return fmt.Errorf("lps25h: address 0x%x is not a 7-bit address", c.addr)
	}
	if c.now == nil {
		return errors.New("lps25h: nil clock")
	}
	if c.pollInterval <= 0 || c.readyTimeout <= 0 {
		return errors.New("lps25h: the poll interval and the ready timeout must be positive")
	}
	if c.mode.continuous && (c.mode.rate < Rate1Hz || c.mode.rate > Rate25Hz) {
		return fmt.Errorf("lps25h: %d is not a continuous rate", c.mode.rate)
	}
	return nil
}

// WithAddress sets the I2C address. The default is DefaultAddress.
func WithAddress(addr uint16) Option { return func(c *config) { c.addr = addr } }

// WithBusName sets the bus name recorded in each measurement's Source, such as
// "/dev/i2c-1". The Sensor does not use it to reach the bus.
func WithBusName(name string) Option { return func(c *config) { c.bus = name } }

// WithClock sets the clock that stamps each measurement. The default is
// time.Now. Tests inject a fixed one.
func WithClock(now func() time.Time) Option { return func(c *config) { c.now = now } }

// WithPollInterval sets how often the status register is read while waiting for
// a conversion. The default is 5 ms.
func WithPollInterval(d time.Duration) Option { return func(c *config) { c.pollInterval = d } }

// WithReadyTimeout sets how long Read waits for a conversion before it gives up
// with vane.ErrNotReady. The default is one second.
func WithReadyTimeout(d time.Duration) Option { return func(c *config) { c.readyTimeout = d } }

// Rate is an output data rate for continuous acquisition. Its value is the code
// the chip takes in bits 6:4 of CTRL_REG1.
type Rate uint8

// The rates the chip offers.
const (
	Rate1Hz   Rate = 1
	Rate7Hz   Rate = 2
	Rate12Hz5 Rate = 3 // 12.5 Hz
	Rate25Hz  Rate = 4
)

// Mode is how the chip acquires samples. The zero value is one-shot.
type Mode struct {
	continuous bool
	rate       Rate
}

// OneShot returns the default mode: each Read triggers one conversion and waits
// for it, so the sample is always new and the chip idles between reads instead
// of heating itself (ADR-0005).
func OneShot() Mode { return Mode{} }

// Continuous returns the mode in which the chip converts at rate on its own and
// Read returns the latest sample. A quantity with no sample newer than the
// previous read is flagged vane.FlagStale; one the chip reports as overwritten
// is flagged vane.FlagOverrun.
func Continuous(rate Rate) Mode { return Mode{continuous: true, rate: rate} }

// WithMode sets the acquisition mode. The default is OneShot.
func WithMode(m Mode) Option { return func(c *config) { c.mode = m } }
