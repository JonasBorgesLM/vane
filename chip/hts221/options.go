package hts221

import (
	"errors"
	"fmt"
	"math/bits"
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
	avg          Averaging
}

// Defaults for waiting on a one-shot conversion. The timeout is a deliberate
// over-estimate: the datasheet gives no conversion times, and on a Pi 5 they
// run from about 7 ms at the default averaging to 51 ms at the most (docs/hardware.md). It only decides how long a failure takes
// to report.
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
		return fmt.Errorf("hts221: address 0x%x is not a 7-bit address", c.addr)
	}
	if c.now == nil {
		return errors.New("hts221: nil clock")
	}
	if c.pollInterval <= 0 || c.readyTimeout <= 0 {
		return errors.New("hts221: the poll interval and the ready timeout must be positive")
	}
	if c.mode.continuous && (c.mode.rate < Rate1Hz || c.mode.rate > Rate12Hz5) {
		return fmt.Errorf("hts221: %d is not a continuous rate", c.mode.rate)
	}
	return c.avg.validate()
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

// Rate is an output data rate for continuous acquisition. Its value is the
// code the chip takes in CTRL_REG1.
type Rate uint8

// The rates the chip offers.
const (
	Rate1Hz   Rate = 1
	Rate7Hz   Rate = 2
	Rate12Hz5 Rate = 3 // 12.5 Hz
)

// Mode is how the chip acquires samples. The zero value is one-shot.
type Mode struct {
	continuous bool
	rate       Rate
}

// OneShot returns the default mode: each Read triggers one conversion and
// waits for it, so the sample is always new and the chip idles between reads
// instead of heating itself (ADR-0005).
func OneShot() Mode { return Mode{} }

// Continuous returns the mode in which the chip converts at rate on its own and
// Read returns the latest sample. A Read that finds no sample newer than the
// previous one flags it vane.FlagStale. The chip has no overrun flag, so
// vane.FlagOverrun is never set.
func Continuous(rate Rate) Mode { return Mode{continuous: true, rate: rate} }

// WithMode sets the acquisition mode. The default is OneShot.
func WithMode(m Mode) Option { return func(c *config) { c.mode = m } }

// Averaging is how many internal samples the chip averages into each output,
// separately for humidity and temperature. More samples means less noise and a
// longer conversion. A zero field keeps the chip's default for that quantity.
type Averaging struct {
	Humidity    uint16 // 4, 8, 16, 32, 64, 128, 256 or 512; default 32
	Temperature uint16 // 2, 4, 8, 16, 32, 64, 128 or 256; default 16
}

const (
	defaultHumidityAvg    = 32
	defaultTemperatureAvg = 16
)

func isPow2Between(v, lo, hi uint16) bool { return v >= lo && v <= hi && v&(v-1) == 0 }

func (a Averaging) validate() error {
	if a.Humidity != 0 && !isPow2Between(a.Humidity, 4, 512) {
		return fmt.Errorf("hts221: %d humidity samples is not one of 4, 8, 16, 32, 64, 128, 256, 512", a.Humidity)
	}
	if a.Temperature != 0 && !isPow2Between(a.Temperature, 2, 256) {
		return fmt.Errorf("hts221: %d temperature samples is not one of 2, 4, 8, 16, 32, 64, 128, 256", a.Temperature)
	}
	return nil
}

// avConf encodes the averaging as the AV_CONF register: AVGH in bits 2:0 counts
// from 4 samples, AVGT in bits 5:3 from 2.
func (a Averaging) avConf() uint8 {
	h, t := a.Humidity, a.Temperature
	if h == 0 {
		h = defaultHumidityAvg
	}
	if t == 0 {
		t = defaultTemperatureAvg
	}
	return uint8(bits.TrailingZeros16(h)-2) | uint8(bits.TrailingZeros16(t)-1)<<3 // #nosec G115 -- validated powers of two, so both fields are 0 to 7
}

// WithAveraging sets the internal averaging. The default is the chip's own, 32
// humidity samples and 16 temperature samples.
func WithAveraging(a Averaging) Option { return func(c *config) { c.avg = a } }
