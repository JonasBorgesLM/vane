package vanetest

import (
	"errors"
	"fmt"
)

// ErrProtocol is returned, wrapped, when a driver breaks a rule of the chip's
// bus protocol.
var ErrProtocol = errors.New("vanetest: protocol violation")

// Reporter is where a violation is reported besides being returned as an error,
// so a driver that swallows the error still fails the test. *testing.T and
// *testing.B satisfy it.
type Reporter interface {
	Errorf(format string, args ...any)
}

const (
	regAutoIncrement = 0x80
	regMask          = 0x7F
	regCount         = 128
)

// Regs is a register file with the rules of an ST MEMS sensor, the building
// block of the chip models. It enforces what a datasheet says and a real chip
// punishes silently:
//
//   - a write to a register outside the writable set is refused (ADR-0007);
//   - a multi-byte read or write needs the auto-increment bit in the
//     sub-address, or a real chip would repeat the first register (G-13);
//   - nothing runs past the last register.
//
// The writable set comes from the datasheet, written by whoever writes the
// model, never from the driver under test. Regs is not safe for concurrent use;
// FakeBus serialises every transaction.
type Regs struct {
	rep      Reporter
	addr     uint16
	mem      [regCount]byte
	writable [regCount]bool
}

// NewRegs returns a register file at addr whose host-writable registers are
// writable. All registers start at zero.
func NewRegs(rep Reporter, addr uint16, writable ...uint8) *Regs {
	r := &Regs{rep: rep, addr: addr}
	for _, w := range writable {
		r.writable[w&regMask] = true
	}
	return r
}

// Addr returns the device address.
func (r *Regs) Addr() uint16 { return r.addr }

// Get returns a register's value. It is the test's view and bypasses the rules.
func (r *Regs) Get(reg uint8) uint8 { return r.mem[reg&regMask] }

// Set stores a value in a register. It is how a model loads factory data and
// samples, and bypasses the rules.
func (r *Regs) Set(reg, val uint8) { r.mem[reg&regMask] = val }

// Tx performs one transaction. A write is w = sub-address followed by data and
// no read; a read is w = sub-address alone and a destination r.
func (r *Regs) Tx(w, rd []byte) error {
	if len(w) == 0 {
		return r.violation("empty write: a transaction starts with a sub-address")
	}
	reg := int(w[0] & regMask)
	auto := w[0]&regAutoIncrement != 0

	if len(rd) == 0 {
		data := w[1:]
		switch {
		case len(data) == 0:
			return r.violation("write to register 0x%02x carries no data", reg)
		case len(data) > 1 && !auto:
			return r.violation("multi-byte write at register 0x%02x without the auto-increment bit", reg)
		case reg+len(data) > regCount:
			return r.violation("write of %d bytes at register 0x%02x runs past the last register", len(data), reg)
		}
		for i := range data {
			if !r.writable[reg+i] {
				return r.violation("write to register 0x%02x, which the datasheet does not let the host write", reg+i)
			}
		}
		copy(r.mem[reg:], data)
		return nil
	}

	switch {
	case len(w) != 1:
		return r.violation("a read sends only the sub-address, got %d bytes", len(w))
	case len(rd) > 1 && !auto:
		return r.violation("multi-byte read at register 0x%02x without the auto-increment bit: a chip returns the same register repeatedly", reg)
	case reg+len(rd) > regCount:
		return r.violation("read of %d bytes at register 0x%02x runs past the last register", len(rd), reg)
	}
	copy(rd, r.mem[reg:])
	return nil
}

func (r *Regs) violation(format string, args ...any) error {
	msg := fmt.Sprintf("device 0x%02x: "+format, append([]any{r.addr}, args...)...)
	if r.rep != nil {
		r.rep.Errorf("%s", msg)
	}
	return fmt.Errorf("%w: %s", ErrProtocol, msg)
}
