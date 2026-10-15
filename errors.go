package vane

import (
	"errors"
	"fmt"
)

// Sentinel errors, one per kind of failure a consumer branches on. They are
// matched with errors.Is and almost always arrive wrapped in a *SensorError.
var (
	// ErrNotPresent: nothing acknowledged the address.
	ErrNotPresent = errors.New("vane: no device at address")
	// ErrUnexpectedChip: the identity register did not hold the expected value.
	// The concrete error is an *IdentityError.
	ErrUnexpectedChip = errors.New("vane: unexpected chip")
	// ErrKernelOwned: a kernel driver is bound to the device, so vane refuses
	// to touch it. The concrete error is a *KernelOwnedError.
	ErrKernelOwned = errors.New("vane: device is owned by a kernel driver")
	// ErrPermission: the bus device could not be opened for lack of permission.
	ErrPermission = errors.New("vane: permission denied on the bus device")
	// ErrCalibration: the chip's factory calibration is degenerate and cannot
	// be used to convert a reading.
	ErrCalibration = errors.New("vane: degenerate factory calibration")
	// ErrNotReady: data-ready did not arrive in time. After a one-shot trigger
	// it is also the symptom of a chip that reset and lost its configuration.
	ErrNotReady = errors.New("vane: data not ready in time")
	// ErrClosed: the sensor or device was used after Close.
	ErrClosed = errors.New("vane: closed")
	// ErrUnsupportedPlatform: the operation needs a platform vane does not
	// support, for example the I2C backend on a non-Linux system.
	ErrUnsupportedPlatform = errors.New("vane: unsupported platform")
)

func (s Source) String() string {
	if s.Bus == "" {
		return fmt.Sprintf("%s@0x%02x", s.Chip, s.Address)
	}
	return fmt.Sprintf("%s@0x%02x on %s", s.Chip, s.Address, s.Bus)
}

// SensorError says where and during what a chip-level failure happened. Err is
// the cause, usually one of the sentinels or a wrapped bus error; Unwrap
// exposes it to errors.Is and errors.As.
type SensorError struct {
	Source Source
	// Op is the step that failed: "identify", "calibrate", "configure",
	// "trigger", "wait", "read" or "close".
	Op string
	// Reg is the register involved when HasReg is set. The zero value of the
	// struct means "no register", not register 0x00.
	Reg    uint8
	HasReg bool
	Err    error
}

func (e *SensorError) Error() string {
	if e.HasReg {
		return fmt.Sprintf("vane: %s: %s register 0x%02x: %v", e.Source, e.Op, e.Reg, e.Err)
	}
	return fmt.Sprintf("vane: %s: %s: %v", e.Source, e.Op, e.Err)
}

func (e *SensorError) Unwrap() error { return e.Err }

// IdentityError reports that a chip's identity register held another value than
// the expected one. No byte was written to the device (RF-01).
type IdentityError struct {
	Source   Source
	Register uint8
	Want     uint8
	Got      uint8
}

func (e *IdentityError) Error() string {
	return fmt.Sprintf("vane: %s: identity register 0x%02x is 0x%02x, want 0x%02x",
		e.Source, e.Register, e.Got, e.Want)
}

// Is reports a match with ErrUnexpectedChip.
func (e *IdentityError) Is(target error) bool { return target == ErrUnexpectedChip }

// KernelOwnedError reports that a kernel driver is bound to the device. Driver
// is the name read from sysfs, so the message says what to unbind or disable.
type KernelOwnedError struct {
	Source Source
	Driver string
}

func (e *KernelOwnedError) Error() string {
	return fmt.Sprintf("vane: %s is owned by the kernel driver %q", e.Source, e.Driver)
}

// Is reports a match with ErrKernelOwned.
func (e *KernelOwnedError) Is(target error) bool { return target == ErrKernelOwned }
