// Package regio reads and writes the 8-bit registers of the ST MEMS sensors on
// the Sense HAT over a vane.Bus.
//
// It is the only place that builds a register sub-address. A multi-byte read
// that does not auto-increment returns the same register n times, and the
// values still look plausible (G-13), so how a device increments is fixed when
// its Dev is made, never per call. The ST chips use one of two schemes:
//
//   - New: the sub-address MSB requests it (HTS221, LPS25H/HB, the LSM9DS1
//     magnetometer). ReadBlock sets that bit.
//   - NewIncrementByRegister: a register bit enables it, and a sub-address
//     with the MSB set is a different register (the LSM9DS1 accelerometer and
//     gyroscope, CTRL_REG8.IF_ADD_INC; on the board, 0x8F read 0x00 instead of
//     WHO_AM_I). ReadBlock sends the bare address, and the driver keeps the
//     register bit set.
//   - NewByteAddressed: the registers are 0x80 to 0xFF, bit 7 is part of the
//     address, and a burst always increments (the TCS3400; board). Registers
//     below 0x80 are refused: outside the map, 0x12 read 0x24 on the board.
package regio

import (
	"context"
	"fmt"

	"github.com/JonasBorgesLM/vane"
)

const (
	// autoIncrement is the sub-address bit that makes a multi-byte transfer walk
	// through consecutive registers.
	autoIncrement = 0x80
	// maxRegister is the highest register number of the ST schemes, where bit 7
	// of the sub-address is not part of it.
	maxRegister = 0x7F
)

// Dev reads and writes the registers of one device. It owns a small scratch
// buffer so a read allocates nothing (RNF-08): a buffer local to a function
// escapes to the heap because it is passed through an interface.
//
// A Dev is not safe for concurrent use. The driver that owns it holds the
// per-chip mutex around every call (ADR-0004), which is also what keeps the
// scratch buffer from being shared.
type Dev struct {
	bus    vane.Bus
	addr   uint16
	inc    uint8 // OR-ed into the sub-address of a block read
	lo, hi uint8 // the registers the device has
	buf    [2]byte
}

// New returns a Dev for a device whose sub-address MSB requests auto-increment.
func New(bus vane.Bus, addr uint16) *Dev {
	return &Dev{bus: bus, addr: addr, inc: autoIncrement, hi: maxRegister}
}

// NewIncrementByRegister returns a Dev for a device whose auto-increment is
// enabled by a register bit, such as the LSM9DS1 accelerometer and gyroscope
// (CTRL_REG8.IF_ADD_INC). Its block reads never set the sub-address MSB. The
// driver must keep the device's increment bit set, or a block read returns one
// register repeated.
func NewIncrementByRegister(bus vane.Bus, addr uint16) *Dev {
	return &Dev{bus: bus, addr: addr, hi: maxRegister}
}

// NewByteAddressed returns a Dev for a device whose registers are 0x80 to 0xFF
// and whose bursts always increment, such as the TCS3400 (ADR-0009). Bit 7 of a
// register number is part of the address and is sent as it is.
func NewByteAddressed(bus vane.Bus, addr uint16) *Dev {
	return &Dev{bus: bus, addr: addr, lo: 0x80, hi: 0xFF}
}

// Read returns the value of register reg.
func (d *Dev) Read(ctx context.Context, reg uint8) (uint8, error) {
	if err := d.check(reg); err != nil {
		return 0, err
	}
	d.buf[0] = reg
	if err := d.bus.Tx(ctx, d.addr, d.buf[:1], d.buf[1:2]); err != nil {
		return 0, err
	}
	return d.buf[1], nil
}

// Write stores val in register reg.
func (d *Dev) Write(ctx context.Context, reg, val uint8) error {
	if err := d.check(reg); err != nil {
		return err
	}
	d.buf[0], d.buf[1] = reg, val
	return d.bus.Tx(ctx, d.addr, d.buf[:2], nil)
}

// ReadBlock fills dst with len(dst) consecutive registers starting at reg, in
// one transaction, incrementing as the Dev's scheme says.
func (d *Dev) ReadBlock(ctx context.Context, reg uint8, dst []byte) error {
	if err := d.check(reg); err != nil {
		return err
	}
	if len(dst) == 0 {
		return fmt.Errorf("regio: empty block read at register 0x%02x", reg)
	}
	d.buf[0] = reg | d.inc
	return d.bus.Tx(ctx, d.addr, d.buf[:1], dst)
}

// check rejects a register the device does not have. On the ST schemes that is
// one using bit 7, which would either be mistaken for an auto-increment request
// or silently change which register is addressed; on a byte-addressed device,
// one below 0x80, outside its map.
func (d *Dev) check(reg uint8) error {
	if reg < d.lo || reg > d.hi {
		return fmt.Errorf("regio: register 0x%02x out of range 0x%02x to 0x%02x", reg, d.lo, d.hi)
	}
	return nil
}
