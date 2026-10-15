//go:build !linux

package i2cdev_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/i2cdev"
)

// Off Linux the package builds and every operation says so (RNF-02, G-16).
func TestEveryOperationReportsAnUnsupportedPlatform(t *testing.T) {
	b, err := i2cdev.Open("/dev/i2c-1")
	if !errors.Is(err, vane.ErrUnsupportedPlatform) || b != nil {
		t.Fatalf("Open = %v, %v; want nil and ErrUnsupportedPlatform", b, err)
	}
	var zero *i2cdev.Bus
	if err := zero.Tx(context.Background(), 0x5F, []byte{0x0F}, make([]byte, 1)); !errors.Is(err, vane.ErrUnsupportedPlatform) {
		t.Errorf("Tx = %v", err)
	}
	if zero.Name() != "" || zero.Close() != nil {
		t.Error("Name and Close on the stub should be empty and harmless")
	}
}
