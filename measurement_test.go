package vane_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
)

// Negative control, seen failing: weakening Flag.Has to `f&x != 0` makes the
// "one of two unset" case fail.
func TestFlagHas(t *testing.T) {
	f := vane.FlagClamped | vane.FlagStale
	tests := []struct {
		name string
		flag vane.Flag
		want bool
	}{
		{"set", vane.FlagClamped, true},
		{"set too", vane.FlagStale, true},
		{"both", vane.FlagClamped | vane.FlagStale, true},
		{"unset", vane.FlagOverrun, false},
		{"one of two unset", vane.FlagStale | vane.FlagOverrun, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.Has(tc.flag); got != tc.want {
				t.Fatalf("Flag(%q).Has(%q) = %v, want %v", f, tc.flag, got, tc.want)
			}
		})
	}
}

func TestFlagsAreDistinctBits(t *testing.T) {
	all := []vane.Flag{vane.FlagClamped, vane.FlagOutOfRange, vane.FlagStale, vane.FlagOverrun}
	var seen vane.Flag
	for _, f := range all {
		if f == 0 || f&(f-1) != 0 {
			t.Fatalf("flag %d is not a single bit", f)
		}
		if seen&f != 0 {
			t.Fatalf("flag %d overlaps an earlier one", f)
		}
		seen |= f
	}
}

func TestFlagString(t *testing.T) {
	tests := []struct {
		flag vane.Flag
		want string
	}{
		{0, ""},
		{vane.FlagClamped, "clamped"},
		{vane.FlagOutOfRange, "out-of-range"},
		{vane.FlagClamped | vane.FlagStale, "clamped|stale"},
		{vane.FlagClamped | vane.FlagOutOfRange | vane.FlagStale | vane.FlagOverrun, "clamped|out-of-range|stale|overrun"},
	}
	for _, tc := range tests {
		if got := tc.flag.String(); got != tc.want {
			t.Errorf("Flag(%d).String() = %q, want %q", uint8(tc.flag), got, tc.want)
		}
	}
}

func ExampleFlag_Has() {
	f := vane.FlagClamped | vane.FlagStale
	fmt.Println(f.Has(vane.FlagClamped), f.Has(vane.FlagOverrun), f)
	// Output: true false clamped|stale
}

func ExampleMeasurement() {
	m := vane.Measurement[vane.Celsius]{
		Value:  21.5,
		Raw:    -1234,
		Source: vane.Source{Chip: vane.ChipHTS221, Bus: "/dev/i2c-1", Address: 0x5F},
		Time:   time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
	}
	fmt.Printf("%.1f °C from %s at 0x%02X (raw %d) at %s\n", float64(m.Value), m.Source.Chip, m.Source.Address, m.Raw, m.Time.Format(time.RFC3339))
	// Output: 21.5 °C from HTS221 at 0x5F (raw -1234) at 2026-10-12T12:00:00Z
}
