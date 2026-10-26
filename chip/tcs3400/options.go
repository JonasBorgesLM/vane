package tcs3400

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
	cycles       int
	gain         Gain
	pollInterval time.Duration
	readyTimeout time.Duration // 0: derived from the integration time
}

func defaultConfig() config {
	return config{
		addr:         DefaultAddress,
		now:          time.Now,
		cycles:       64,     // ATIME 0xC0, 178 ms: the datasheet's characterisation condition (p. 8)
		gain:         Gain16, // likewise
		pollInterval: 5 * time.Millisecond,
	}
}

// Gain is the RGBC analog gain.
type Gain uint8

// The gains the chip offers (CONTROL.AGAIN, p. 18).
const (
	Gain1  Gain = 1
	Gain4  Gain = 4
	Gain16 Gain = 16
	Gain64 Gain = 64
)

var gainCodes = map[Gain]uint8{Gain1: 0b00, Gain4: 0b01, Gain16: 0b10, Gain64: 0b11}

// integration is the time one cycle count takes.
func (c *config) integration() time.Duration {
	return time.Duration(c.cycles) * cycle * time.Microsecond
}

// timeout is how long Read waits for a cycle: the integration time twice, plus
// 20 ms, unless WithReadyTimeout says otherwise.
func (c *config) timeout() time.Duration {
	if c.readyTimeout > 0 {
		return c.readyTimeout
	}
	return 2*c.integration() + 20*time.Millisecond
}

// maxCount is the highest count at the integration time (p. 15).
func (c *config) maxCount() uint32 {
	return min(uint32(c.cycles)*1024, 65535) // #nosec G115 -- cycles is 1 to 256
}

func (c *config) validate() error {
	switch {
	case c.addr > 0x7F:
		return fmt.Errorf("tcs3400: address 0x%x is not a 7-bit address", c.addr)
	case c.now == nil:
		return errors.New("tcs3400: nil clock")
	case c.cycles < 1 || c.cycles > 256:
		return fmt.Errorf("tcs3400: %d integration cycles is not 1 to 256", c.cycles)
	case c.pollInterval <= 0 || c.readyTimeout < 0:
		return errors.New("tcs3400: the poll interval must be positive and the ready timeout not negative")
	}
	if _, ok := gainCodes[c.gain]; !ok {
		return fmt.Errorf("tcs3400: gain %d is not 1, 4, 16 or 64", c.gain)
	}
	return nil
}

// WithAddress sets the I2C address: DefaultAddress (TCS34001/3) or AltAddress
// (TCS34005/7).
func WithAddress(addr uint16) Option { return func(c *config) { c.addr = addr } }

// WithBusName sets the bus name recorded in each measurement's Source.
func WithBusName(name string) Option { return func(c *config) { c.bus = name } }

// WithClock sets the clock that stamps each measurement. The default is
// time.Now.
func WithClock(now func() time.Time) Option { return func(c *config) { c.now = now } }

// WithIntegration sets the integration time in cycles of 2.78 ms, 1 to 256
// (2.78 ms to 712 ms). The default is 64, 178 ms. Longer integration counts more
// light and makes Read slower; the maximum count is min(cycles × 1024, 65535).
func WithIntegration(cycles int) Option { return func(c *config) { c.cycles = cycles } }

// WithGain sets the analog gain. The default is Gain16.
func WithGain(g Gain) Option { return func(c *config) { c.gain = g } }

// WithPollInterval sets how often the status register is read while waiting for
// a cycle. The default is 5 ms.
func WithPollInterval(d time.Duration) Option { return func(c *config) { c.pollInterval = d } }

// WithReadyTimeout sets how long Read waits for a cycle before vane.ErrNotReady.
// The default is twice the integration time plus 20 ms.
func WithReadyTimeout(d time.Duration) Option { return func(c *config) { c.readyTimeout = d } }
