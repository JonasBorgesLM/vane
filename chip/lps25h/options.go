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
