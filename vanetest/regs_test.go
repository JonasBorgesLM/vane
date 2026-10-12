package vanetest_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

// recorder is a Reporter that collects violations instead of failing the test,
// so a test can assert that a rule fires.
type recorder struct{ msgs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func newRegs(rep vanetest.Reporter) *vanetest.Regs {
	r := vanetest.NewRegs(rep, 0x5F, 0x20, 0x21, 0x22)
	for i := 0; i < 128; i++ {
		r.Set(uint8(i), uint8(i)^0x5A)
	}
	return r
}

func TestRegsServeReadsAndWritableWrites(t *testing.T) {
	rec := &recorder{}
	r := newRegs(rec)

	single := make([]byte, 1)
	if err := r.Tx([]byte{0x0F}, single); err != nil || single[0] != r.Get(0x0F) {
		t.Fatalf("single read = %#x, %v", single[0], err)
	}
	block := make([]byte, 3)
	if err := r.Tx([]byte{0x28 | 0x80}, block); err != nil {
		t.Fatal(err)
	}
	for i, b := range block {
		if b != r.Get(uint8(0x28+i)) {
			t.Errorf("block[%d] = %#x, want register 0x%02x", i, b, 0x28+i)
		}
	}
	if err := r.Tx([]byte{0x20, 0x85}, nil); err != nil || r.Get(0x20) != 0x85 {
		t.Fatalf("write: %v, register = %#x", err, r.Get(0x20))
	}
	if err := r.Tx([]byte{0x20 | 0x80, 1, 2, 3}, nil); err != nil || r.Get(0x22) != 3 {
		t.Fatalf("multi-byte write with auto-increment: %v, register 0x22 = %#x", err, r.Get(0x22))
	}
	if len(rec.msgs) != 0 {
		t.Fatalf("legal traffic reported violations: %v", rec.msgs)
	}
}

// Each rule below must fire: return ErrProtocol, report it, and leave the
// register file untouched.
func TestRegsRulesFire(t *testing.T) {
	tests := []struct {
		name string
		w    []byte
		rd   []byte
		want string
	}{
		{"write outside the writable set", []byte{0x30, 1}, nil, "does not let the host write"},
		{"write outside the set, reached by auto-increment", []byte{0x22 | 0x80, 1, 2}, nil, "register 0x23"},
		{"multi-byte read without the bit", []byte{0x28}, make([]byte, 2), "without the auto-increment bit"},
		{"multi-byte write without the bit", []byte{0x20, 1, 2}, nil, "without the auto-increment bit"},
		{"read past the last register", []byte{0x7E | 0x80}, make([]byte, 4), "runs past"},
		{"write past the last register", []byte{0x7E | 0x80, 1, 2, 3}, nil, "runs past"},
		{"empty write", nil, make([]byte, 1), "empty write"},
		{"write without data", []byte{0x20}, nil, "carries no data"},
		{"read that sends more than the sub-address", []byte{0x0F, 0x01}, make([]byte, 1), "only the sub-address"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			r := newRegs(rec)
			before := [3]byte{r.Get(0x20), r.Get(0x21), r.Get(0x22)}
			err := r.Tx(tc.w, tc.rd)
			if !errors.Is(err, vanetest.ErrProtocol) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want ErrProtocol mentioning %q", err, tc.want)
			}
			if len(rec.msgs) != 1 {
				t.Errorf("reported %d violations, want 1: %v", len(rec.msgs), rec.msgs)
			}
			if after := [3]byte{r.Get(0x20), r.Get(0x21), r.Get(0x22)}; after != before {
				t.Errorf("a refused write changed the registers: %v -> %v", before, after)
			}
		})
	}
}

func TestRegsWithoutAReporterStillReturnTheError(t *testing.T) {
	r := newRegs(nil)
	if err := r.Tx([]byte{0x30, 1}, nil); !errors.Is(err, vanetest.ErrProtocol) {
		t.Fatalf("error = %v", err)
	}
}
