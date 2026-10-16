package lsm9ds1

import (
	"errors"
	"fmt"
	"time"
)

// Option configures a Sensor.
type Option func(*config)

type config struct {
	agAddr, magAddr uint16
	bus             string
	now             func() time.Time
	rate            Rate
	accel           AccelRange
	gyro            GyroRange
	mag             MagRange
	fifo            bool
}

func defaultConfig() config {
	return config{
		agAddr:  DefaultAccelGyroAddress,
		magAddr: DefaultMagnetometerAddress,
		now:     time.Now,
		rate:    Rate119Hz,
		accel:   Accel2G,
		gyro:    Gyro245DPS,
		mag:     Mag4Gauss,
	}
}

// Rate is the output data rate of the accelerometer and gyroscope, which run
// together at the gyroscope's rate (DocID025715 Rev 3 p. 19). Its value is the
// ODR_G code.
type Rate uint8

// period is the time between two samples at the rate.
func (r Rate) period() time.Duration {
	hz := [...]float64{0, 14.9, 59.5, 119, 238, 476, 952}[r]
	return time.Duration(float64(time.Second) / hz)
}

// The rates the chip offers with both sensors on.
const (
	Rate14Hz9 Rate = 1 // 14.9 Hz
	Rate59Hz5 Rate = 2 // 59.5 Hz
	Rate119Hz Rate = 3
	Rate238Hz Rate = 4
	Rate476Hz Rate = 5
	Rate952Hz Rate = 6
)

// AccelRange is the accelerometer full scale, in g.
type AccelRange uint8

// GyroRange is the gyroscope full scale, in degrees per second.
type GyroRange uint16

// MagRange is the magnetometer full scale, in gauss.
type MagRange uint8

// Full scales. The default of each is the chip's reset value, its narrowest.
const (
	Accel2G  AccelRange = 2
	Accel4G  AccelRange = 4
	Accel8G  AccelRange = 8
	Accel16G AccelRange = 16

	Gyro245DPS  GyroRange = 245
	Gyro500DPS  GyroRange = 500
	Gyro2000DPS GyroRange = 2000

	Mag4Gauss  MagRange = 4
	Mag8Gauss  MagRange = 8
	Mag12Gauss MagRange = 12
	Mag16Gauss MagRange = 16
)

// scale is a full scale's register code and sensitivity per count, from
// DocID025715 Rev 3: codes on pp. 45, 52 and 64, sensitivities in Table 3,
// p. 12 (mg, mdps and mgauss per LSB, here in g, dps and gauss).
type scale struct {
	code uint8
	per  float64
}

var (
	accelScales = map[AccelRange]scale{
		Accel2G:  {0b00, 0.061e-3},
		Accel16G: {0b01, 0.732e-3},
		Accel4G:  {0b10, 0.122e-3},
		Accel8G:  {0b11, 0.244e-3},
	}
	gyroScales = map[GyroRange]scale{
		Gyro245DPS:  {0b00, 8.75e-3},
		Gyro500DPS:  {0b01, 17.5e-3},
		Gyro2000DPS: {0b11, 70e-3},
	}
	magScales = map[MagRange]scale{
		Mag4Gauss:  {0b00, 0.14e-3},
		Mag8Gauss:  {0b01, 0.29e-3},
		Mag12Gauss: {0b10, 0.43e-3},
		Mag16Gauss: {0b11, 0.58e-3},
	}
)

func (c *config) validate() error {
	switch {
	case c.agAddr > 0x7F || c.magAddr > 0x7F:
		return fmt.Errorf("lsm9ds1: address 0x%x or 0x%x is not a 7-bit address", c.agAddr, c.magAddr)
	case c.agAddr == c.magAddr:
		return fmt.Errorf("lsm9ds1: the accelerometer/gyroscope and the magnetometer cannot share address 0x%x", c.agAddr)
	case c.now == nil:
		return errors.New("lsm9ds1: nil clock")
	case c.rate < Rate14Hz9 || c.rate > Rate952Hz:
		return fmt.Errorf("lsm9ds1: %d is not a rate code", c.rate)
	}
	if _, ok := accelScales[c.accel]; !ok {
		return fmt.Errorf("lsm9ds1: ±%d g is not an accelerometer full scale (2, 4, 8, 16)", c.accel)
	}
	if _, ok := gyroScales[c.gyro]; !ok {
		return fmt.Errorf("lsm9ds1: ±%d dps is not a gyroscope full scale (245, 500, 2000)", c.gyro)
	}
	if _, ok := magScales[c.mag]; !ok {
		return fmt.Errorf("lsm9ds1: ±%d gauss is not a magnetometer full scale (4, 8, 12, 16)", c.mag)
	}
	return nil
}

// WithAccelGyroAddress sets the accelerometer and gyroscope address. The
// default is DefaultAccelGyroAddress.
func WithAccelGyroAddress(addr uint16) Option { return func(c *config) { c.agAddr = addr } }

// WithMagnetometerAddress sets the magnetometer address. The default is
// DefaultMagnetometerAddress.
func WithMagnetometerAddress(addr uint16) Option { return func(c *config) { c.magAddr = addr } }

// WithBusName sets the bus name recorded in each measurement's Source.
func WithBusName(name string) Option { return func(c *config) { c.bus = name } }

// WithClock sets the clock that stamps each measurement. The default is
// time.Now.
func WithClock(now func() time.Time) Option { return func(c *config) { c.now = now } }

// WithRate sets the accelerometer and gyroscope rate. The default is Rate119Hz.
func WithRate(r Rate) Option { return func(c *config) { c.rate = r } }

// WithAccelRange sets the accelerometer full scale. The default is Accel2G.
func WithAccelRange(r AccelRange) Option { return func(c *config) { c.accel = r } }

// WithGyroRange sets the gyroscope full scale. The default is Gyro245DPS.
func WithGyroRange(r GyroRange) Option { return func(c *config) { c.gyro = r } }

// WithMagRange sets the magnetometer full scale. The default is Mag4Gauss.
func WithMagRange(r MagRange) Option { return func(c *config) { c.mag = r } }

// WithFIFO runs the accelerometer and gyroscope through the chip's 32-sample
// FIFO, in Continuous mode, and makes ReadBatch the way to read them: every
// sample, oldest first, instead of the latest one (ADR-0008). Read then
// returns ErrWrongMode. The magnetometer is not in the FIFO and ReadMagnetic
// works either way.
func WithFIFO() Option { return func(c *config) { c.fifo = true } }
