package vane_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane"
)

var src = vane.Source{Chip: vane.ChipHTS221, Bus: "/dev/i2c-1", Address: 0x5F}

func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{
		vane.ErrNotPresent, vane.ErrUnexpectedChip, vane.ErrKernelOwned, vane.ErrPermission,
		vane.ErrCalibration, vane.ErrNotReady, vane.ErrClosed, vane.ErrUnsupportedPlatform,
	}
	for i, a := range all {
		for j, b := range all {
			if (i == j) != errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", a, b, errors.Is(a, b), i == j)
			}
		}
	}
}

// Negative control, seen failing: deleting IdentityError.Is makes the
// ErrUnexpectedChip case fail; same for KernelOwnedError and ErrKernelOwned.
func TestTypedErrorsMatchTheirSentinelAndTypeWhenWrapped(t *testing.T) {
	identity := &vane.IdentityError{Source: src, Register: 0x0F, Want: 0xBC, Got: 0xBD}
	owned := &vane.KernelOwnedError{Source: vane.Source{Chip: vane.ChipLPS25H, Bus: "/dev/i2c-1", Address: 0x5C}, Driver: "st-press-i2c"}

	tests := []struct {
		name     string
		err      error
		sentinel error
		asTarget any
	}{
		{"identity", identity, vane.ErrUnexpectedChip, new(*vane.IdentityError)},
		{"kernel owned", owned, vane.ErrKernelOwned, new(*vane.KernelOwnedError)},
		{"identity in SensorError", &vane.SensorError{Source: src, Op: "identify", Err: identity}, vane.ErrUnexpectedChip, new(*vane.IdentityError)},
		{"kernel owned in SensorError", &vane.SensorError{Source: owned.Source, Op: "identify", Err: owned}, vane.ErrKernelOwned, new(*vane.KernelOwnedError)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("opening board: %w", tc.err)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(%v, %v) = false", wrapped, tc.sentinel)
			}
			if !errors.As(wrapped, tc.asTarget) {
				t.Errorf("errors.As(%v, %T) = false", wrapped, tc.asTarget)
			}
		})
	}
}

func TestIdentityErrorDoesNotMatchOtherSentinels(t *testing.T) {
	err := &vane.IdentityError{Source: src, Register: 0x0F, Want: 0xBC, Got: 0xBD}
	for _, other := range []error{vane.ErrKernelOwned, vane.ErrNotPresent, vane.ErrClosed} {
		if errors.Is(err, other) {
			t.Errorf("IdentityError matches %v", other)
		}
	}
}

func TestSensorErrorUnwrapsItsCause(t *testing.T) {
	for _, cause := range []error{vane.ErrNotPresent, vane.ErrNotReady, vane.ErrCalibration, vane.ErrClosed} {
		err := fmt.Errorf("env: %w", &vane.SensorError{Source: src, Op: "read", Err: cause})
		if !errors.Is(err, cause) {
			t.Errorf("errors.Is(%v, %v) = false", err, cause)
		}
		var se *vane.SensorError
		if !errors.As(err, &se) || se.Source != src || se.Op != "read" {
			t.Errorf("errors.As lost the SensorError: %+v", se)
		}
	}
}

func TestMessagesCarryChipAddressAndRegisterOrOperation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{"sensor with register", &vane.SensorError{Source: src, Op: "read", Reg: 0x28, HasReg: true, Err: vane.ErrNotReady},
			[]string{"HTS221", "0x5f", "/dev/i2c-1", "read", "register 0x28", "data not ready"}},
		{"sensor register zero is still a register", &vane.SensorError{Source: src, Op: "read", Reg: 0, HasReg: true, Err: vane.ErrNotReady},
			[]string{"register 0x00"}},
		{"sensor without register", &vane.SensorError{Source: src, Op: "wait", Err: vane.ErrNotReady},
			[]string{"HTS221", "0x5f", "wait"}},
		{"identity", &vane.IdentityError{Source: src, Register: 0x0F, Want: 0xBC, Got: 0xBD},
			[]string{"HTS221", "0x5f", "0x0f", "0xbd", "want 0xbc"}},
		{"kernel owned", &vane.KernelOwnedError{Source: vane.Source{Chip: vane.ChipLPS25H, Address: 0x5C}, Driver: "st-press-i2c"},
			[]string{"LPS25H", "0x5c", "st-press-i2c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message %q lacks %q", msg, w)
				}
			}
		})
	}
	noReg := (&vane.SensorError{Source: src, Op: "wait", Err: vane.ErrNotReady}).Error()
	if strings.Contains(noReg, "register") {
		t.Errorf("message without a register mentions one: %q", noReg)
	}
}

func ExampleIdentityError() {
	err := fmt.Errorf("open: %w", &vane.IdentityError{
		Source:   vane.Source{Chip: vane.ChipHTS221, Bus: "/dev/i2c-1", Address: 0x5F},
		Register: 0x0F, Want: 0xBC, Got: 0xBD,
	})
	var id *vane.IdentityError
	fmt.Println(errors.Is(err, vane.ErrUnexpectedChip), errors.As(err, &id) && id.Got == 0xBD)
	fmt.Println(err)
	// Output:
	// true true
	// open: vane: HTS221@0x5f on /dev/i2c-1: identity register 0x0f is 0xbd, want 0xbc
}
