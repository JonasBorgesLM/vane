package regio_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane/internal/regio"
)

// registerIncFake behaves like the LSM9DS1 accelerometer and gyroscope block:
// auto-increment is a register bit (CTRL_REG8.IF_ADD_INC), and a sub-address
// with bit 7 set is a different register. On the board, 0x8F read 0x00 instead
// of WHO_AM_I (docs/hardware.md), so here it is an error.
type registerIncFake struct {
	regs  [128]byte
	lastW []byte
}

func (f *registerIncFake) Tx(ctx context.Context, a uint16, w, r []byte) error {
	f.lastW = append(f.lastW[:0], w...)
	if err := ctx.Err(); err != nil {
		return err
	}
	if a != addr {
		return errors.New("fake: wrong address")
	}
	if len(w) > 0 && w[0]&0x80 != 0 {
		return errors.New("fake: sub-address with bit 7 set addresses another register")
	}
	switch {
	case len(w) == 2 && len(r) == 0:
		f.regs[w[0]] = w[1]
	case len(w) == 1 && len(r) >= 1:
		copy(r, f.regs[w[0]:])
	default:
		return errors.New("fake: unexpected transaction shape")
	}
	return nil
}

func newRegisterIncFake() *registerIncFake {
	f := &registerIncFake{}
	for i := range f.regs {
		f.regs[i] = byte(i) ^ 0x5A
	}
	return f
}

// Negative control: built with New, which sets bit 7, the same read fails.
func TestRegisterIncrementReadBlockSendsTheBareAddress(t *testing.T) {
	f := newRegisterIncFake()
	dst := make([]byte, 12)
	if err := regio.NewIncrementByRegister(f, addr).ReadBlock(context.Background(), 0x18, dst); err != nil {
		t.Fatal(err)
	}
	if f.lastW[0] != 0x18 {
		t.Fatalf("sub-address = %#x, want the bare 0x18", f.lastW[0])
	}
	for i, b := range dst {
		if want := f.regs[0x18+i]; b != want {
			t.Errorf("dst[%d] = %#x, want register 0x%02x = %#x", i, b, 0x18+i, want)
		}
	}
	if err := regio.New(f, addr).ReadBlock(context.Background(), 0x18, dst); err == nil {
		t.Error("the MSB scheme reached the register-increment device without error: the fake does not guard what it should")
	}
}

func TestRegisterIncrementKeepsTheSingleRegisterRules(t *testing.T) {
	f := newRegisterIncFake()
	d := regio.NewIncrementByRegister(f, addr)
	ctx := context.Background()
	if v, err := d.Read(ctx, 0x0F); err != nil || v != f.regs[0x0F] {
		t.Fatalf("Read = %#x, %v", v, err)
	}
	if err := d.Write(ctx, 0x22, 0x44); err != nil || f.regs[0x22] != 0x44 {
		t.Fatalf("Write: %v, register = %#x", err, f.regs[0x22])
	}
	if err := d.ReadBlock(ctx, 0x80, make([]byte, 2)); err == nil {
		t.Error("a register above 0x7F was accepted")
	}
	if err := d.ReadBlock(ctx, 0x18, nil); err == nil {
		t.Error("an empty block was accepted")
	}
}

func TestRegisterIncrementReadBlockDoesNotAllocate(t *testing.T) {
	d := regio.NewIncrementByRegister(newRegisterIncFake(), addr)
	ctx := context.Background()
	dst := make([]byte, 12)
	if n := testing.AllocsPerRun(200, func() { _ = d.ReadBlock(ctx, 0x18, dst) }); n != 0 {
		t.Errorf("ReadBlock allocates %v times per call, want 0", n)
	}
}
