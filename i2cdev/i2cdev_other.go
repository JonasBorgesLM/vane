//go:build !linux

package i2cdev

import (
	"context"
	"fmt"
	"runtime"

	"github.com/JonasBorgesLM/vane"
)

// Bus is the Linux I2C backend, which this platform does not have. It exists so
// that code using it compiles everywhere; every operation reports
// vane.ErrUnsupportedPlatform.
type Bus struct{}

var _ vane.Bus = (*Bus)(nil)

// Open always fails on this platform.
func Open(path string) (*Bus, error) {
	return nil, fmt.Errorf("%w: the I2C backend needs Linux, not %s, to open %s", vane.ErrUnsupportedPlatform, runtime.GOOS, path)
}

// Tx always fails on this platform.
func (*Bus) Tx(context.Context, uint16, []byte, []byte) error { return vane.ErrUnsupportedPlatform }

// Name returns an empty name: there is no device.
func (*Bus) Name() string { return "" }

// Close does nothing.
func (*Bus) Close() error { return nil }
