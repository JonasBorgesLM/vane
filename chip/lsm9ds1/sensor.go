package lsm9ds1

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/internal/regio"
)

// Vector3 is one three-axis reading. Each axis is a full measurement, with its
// raw count, source, time and flags; the three share a Time.
type Vector3[T ~float64] struct {
	X, Y, Z vane.Measurement[T]
}

// Motion is one accelerometer and gyroscope sample. The two come from one
// output read and share a Time.
type Motion struct {
	Acceleration Vector3[vane.StandardGravity]
	AngularRate  Vector3[vane.DegreesPerSecond]
}

// Sensor is an LSM9DS1 on a bus. It is safe for concurrent use: a mutex makes
// every register sequence one per chip (ADR-0004).
type Sensor struct {
	ag, mag       *regio.Dev
	agSrc, magSrc vane.Source
	cfg           config

	mu     sync.Mutex
	agErr  error // why the accelerometer and gyroscope are unusable; nil when they work
	magErr error // likewise for the magnetometer
	out    [12]byte
	closed bool
}

// New identifies both devices, then configures each one that identified. A
// device is written to only after its own identity register has been read and
// matched (RF-01). New fails only when neither device is usable; otherwise the
// unusable one's reads return its error (ADR-0006).
func New(ctx context.Context, bus vane.Bus, opts ...Option) (*Sensor, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Sensor{
		// The accelerometer and gyroscope increment by CTRL_REG8.IF_ADD_INC, and an
		// MSB-set sub-address reaches another register there (docs/hardware.md).
		ag:     regio.NewIncrementByRegister(bus, cfg.agAddr),
		mag:    regio.New(bus, cfg.magAddr),
		agSrc:  vane.Source{Chip: vane.ChipLSM9DS1, Bus: cfg.bus, Address: cfg.agAddr},
		magSrc: vane.Source{Chip: vane.ChipLSM9DS1, Bus: cfg.bus, Address: cfg.magAddr},
		cfg:    cfg,
	}
	s.agErr = s.identify(ctx, s.ag, s.agSrc, agWhoAmIValue)
	if s.agErr == nil {
		s.agErr = s.configureAccelGyro(ctx)
	}
	s.magErr = s.identify(ctx, s.mag, s.magSrc, magWhoAmIValue)
	if s.magErr == nil {
		s.magErr = s.configureMagnetometer(ctx)
	}
	if s.agErr != nil && s.magErr != nil {
		return nil, errors.Join(s.agErr, s.magErr)
	}
	return s, nil
}

func (s *Sensor) identify(ctx context.Context, d *regio.Dev, src vane.Source, want uint8) error {
	id, err := d.Read(ctx, regWhoAmI)
	if err != nil {
		return fail(src, "identify", regWhoAmI, err)
	}
	if id != want {
		return &vane.IdentityError{Source: src, Register: regWhoAmI, Want: want, Got: id}
	}
	return nil
}

func fail(src vane.Source, op string, reg uint8, err error) error {
	return &vane.SensorError{Source: src, Op: op, Reg: reg, HasReg: true, Err: err}
}

// configureAccelGyro writes every register the reads rely on, rather than
// trusting reset values: on the board the magnetometer held a configuration
// nobody had written in this boot (docs/hardware.md). CTRL_REG8 goes first, so
// BDU and IF_ADD_INC are set before any block read; CTRL_REG1_G goes last,
// because writing it is what runs both sensors together at its rate (p. 19).
func (s *Sensor) configureAccelGyro(ctx context.Context) error {
	ctrl6 := uint8(s.cfg.rate)<<5 | accelScales[s.cfg.accel].code<<3
	ctrl1 := uint8(s.cfg.rate)<<5 | gyroScales[s.cfg.gyro].code<<3
	for _, w := range [...]struct{ reg, val uint8 }{
		{agCtrl8, agCtrl8Value},
		{agFIFOCtrl, 0}, // Bypass: Read wants the latest sample, not the oldest
		{agCtrl9, 0},
		{agCtrl6XL, ctrl6},
		{agCtrl1G, ctrl1},
	} {
		if err := s.ag.Write(ctx, w.reg, w.val); err != nil {
			return fail(s.agSrc, "configure", w.reg, err)
		}
	}
	return nil
}

func (s *Sensor) configureMagnetometer(ctx context.Context) error {
	for _, w := range [...]struct{ reg, val uint8 }{
		{magCtrl5, magCtrl5Value},
		{magCtrl2, magScales[s.cfg.mag].code << 5},
		{magCtrl1, magCtrl1Value},
		{magCtrl3, magContinuous},
	} {
		if err := s.mag.Write(ctx, w.reg, w.val); err != nil {
			return fail(s.magSrc, "configure", w.reg, err)
		}
	}
	return nil
}

// Read returns the latest accelerometer and gyroscope sample. A quantity with no
// sample newer than the previous Read is flagged vane.FlagStale.
func (s *Sensor) Read(ctx context.Context) (Motion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(s.agSrc, s.agErr); err != nil {
		return Motion{}, err
	}
	st, err := s.ag.Read(ctx, agStatus)
	if err != nil {
		return Motion{}, fail(s.agSrc, "read", agStatus, err)
	}
	now := s.cfg.now()
	if err := s.ag.ReadBlock(ctx, agOut, s.out[:12]); err != nil {
		return Motion{}, fail(s.agSrc, "read", agOut, err)
	}
	var gFlags, xlFlags vane.Flag
	if st&agStatusGDA == 0 {
		gFlags |= vane.FlagStale
	}
	if st&agStatusXLDA == 0 {
		xlFlags |= vane.FlagStale
	}
	return Motion{
		AngularRate:  vector[vane.DegreesPerSecond](s.out[0:6], gyroScales[s.cfg.gyro].per, s.agSrc, now, gFlags),
		Acceleration: vector[vane.StandardGravity](s.out[6:12], accelScales[s.cfg.accel].per, s.agSrc, now, xlFlags),
	}, nil
}

// ReadMagnetic returns the latest magnetometer sample. It is flagged
// vane.FlagStale when no newer sample exists and vane.FlagOverrun when the chip
// overwrote one nobody read.
func (s *Sensor) ReadMagnetic(ctx context.Context) (Vector3[vane.Gauss], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(s.magSrc, s.magErr); err != nil {
		return Vector3[vane.Gauss]{}, err
	}
	st, err := s.mag.Read(ctx, magStatus)
	if err != nil {
		return Vector3[vane.Gauss]{}, fail(s.magSrc, "read", magStatus, err)
	}
	now := s.cfg.now()
	if err := s.mag.ReadBlock(ctx, magOut, s.out[:6]); err != nil {
		return Vector3[vane.Gauss]{}, fail(s.magSrc, "read", magOut, err)
	}
	var flags vane.Flag
	if st&magStatusZYXDA == 0 {
		flags |= vane.FlagStale
	}
	if st&magStatusZYXOR != 0 {
		flags |= vane.FlagOverrun
	}
	return vector[vane.Gauss](s.out[0:6], magScales[s.cfg.mag].per, s.magSrc, now, flags), nil
}

func (s *Sensor) usable(src vane.Source, deviceErr error) error {
	if s.closed {
		return &vane.SensorError{Source: src, Op: "read", Err: vane.ErrClosed}
	}
	return deviceErr
}

// vector converts three little-endian 16-bit counts. A count at either end of
// its range is where the chip saturates, so the axis is flagged
// vane.FlagClamped: the true value is at least that large.
func vector[T ~float64](b []byte, per float64, src vane.Source, at time.Time, flags vane.Flag) Vector3[T] {
	axis := func(i int) vane.Measurement[T] {
		raw := int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8) // #nosec G115 -- reinterpreting two's complement is the point
		f := flags
		if raw == math.MaxInt16 || raw == math.MinInt16 {
			f |= vane.FlagClamped
		}
		return vane.Measurement[T]{Value: T(float64(raw) * per), Raw: int32(raw), Source: src, Time: at, Flags: f}
	}
	return Vector3[T]{X: axis(0), Y: axis(1), Z: axis(2)}
}

// Close powers both devices down. It is idempotent; a read after it returns
// vane.ErrClosed. A device that never identified is not written to.
func (s *Sensor) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	if s.agErr == nil {
		for _, reg := range [...]uint8{agCtrl1G, agCtrl6XL} {
			if err := s.ag.Write(ctx, reg, 0); err != nil {
				errs = append(errs, fail(s.agSrc, "close", reg, err))
				break
			}
		}
	}
	if s.magErr == nil {
		if err := s.mag.Write(ctx, magCtrl3, magPowerDown); err != nil {
			errs = append(errs, fail(s.magSrc, "close", magCtrl3, err))
		}
	}
	return errors.Join(errs...)
}
