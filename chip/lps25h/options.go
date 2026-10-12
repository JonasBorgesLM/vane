package lps25h

import (
	"errors"
	"fmt"
	"time"
)

// Option configures a Sensor.
type Option func(*config)

type config struct {
	addr uint16
	bus  string
	now  func() time.Time
}

func defaultConfig() config {
	return config{addr: DefaultAddress, now: time.Now}
}

func (c *config) validate() error {
	if c.addr > 0x7F {
		return fmt.Errorf("lps25h: address 0x%x is not a 7-bit address", c.addr)
	}
	if c.now == nil {
		return errors.New("lps25h: nil clock")
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
