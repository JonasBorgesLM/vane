package regio_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane/internal/regio"
)

// byteAddrFake behaves like the TCS3400: registers 0x80 to 0xFF, bit 7 part of
// the address, every burst incrementing. Below 0x80 is outside the map; on the
// board 0x12 read 0x24, so here it is an error.
type byteAddrFake struct {
	regs  [256]byte
	lastW []byte
}

func (f *byteAddrFake) Tx(ctx context.Context, a uint16, w, r []byte) error {
	f.lastW = append(f.lastW[:0], w...)
	if err := ctx.Err(); err != nil {
		return err
	}
	if a != addr {
		return errors.New("fake: wrong address")
	}
	if len(w) == 0 || w[0] < 0x80 {
		return errors.New("fake: sub-address below 0x80 is outside the register map")
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

func newByteAddrFake() *byteAddrFake {
	f := &byteAddrFake{}
	for i := range f.regs {
		f.regs[i] = byte(i) ^ 0x3C
	}
	return f
}

// Negative control: built with New, the same reads are refused, since New's
// registers end at 0x7F.
func TestByteAddressedReadsAndWritesTheFullAddress(t *testing.T) {
	f := newByteAddrFake()
	d := regio.NewByteAddressed(f, addr)
	ctx := context.Background()
	if v, err := d.Read(ctx, 0x92); err != nil || v != f.regs[0x92] || f.lastW[0] != 0x92 {
		t.Fatalf("Read(0x92) = %#x, %v; sub-address sent %#x", v, err, f.lastW[0])
	}
	if err := d.Write(ctx, 0x80, 0x03); err != nil || f.regs[0x80] != 0x03 || f.lastW[0] != 0x80 {
		t.Fatalf("Write(0x80): %v, register %#x, sub-address %#x", err, f.regs[0x80], f.lastW[0])
	}
	dst := make([]byte, 8)
	if err := d.ReadBlock(ctx, 0x94, dst); err != nil || f.lastW[0] != 0x94 {
		t.Fatalf("ReadBlock(0x94): %v, sub-address %#x", err, f.lastW[0])
	}
	for i, b := range dst {
		if want := f.regs[0x94+i]; b != want {
			t.Errorf("dst[%d] = %#x, want register 0x%02x = %#x", i, b, 0x94+i, want)
		}
	}
	if _, err := regio.New(f, addr).Read(ctx, 0x92); err == nil {
		t.Error("New accepted register 0x92: the ST schemes end at 0x7F")
	}
}

func TestByteAddressedRefusesRegistersBelow0x80BeforeTheBus(t *testing.T) {
	f := newByteAddrFake()
	d := regio.NewByteAddressed(f, addr)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"Read":      func() error { _, err := d.Read(ctx, 0x12); return err },
		"Write":     func() error { return d.Write(ctx, 0x7F, 0) },
		"ReadBlock": func() error { return d.ReadBlock(ctx, 0x00, make([]byte, 2)) },
	} {
		f.lastW = nil
		if err := call(); err == nil {
			t.Errorf("%s below 0x80 accepted", name)
		}
		if f.lastW != nil {
			t.Errorf("%s below 0x80 reached the bus", name)
		}
	}
	if err := d.ReadBlock(ctx, 0x94, nil); err == nil {
		t.Error("an empty block was accepted")
	}
}

func TestByteAddressedDoesNotAllocate(t *testing.T) {
	d := regio.NewByteAddressed(newByteAddrFake(), addr)
	ctx := context.Background()
	dst := make([]byte, 8)
	for name, fn := range map[string]func(){
		"Read":      func() { _, _ = d.Read(ctx, 0x93) },
		"Write":     func() { _ = d.Write(ctx, 0x80, 0) },
		"ReadBlock": func() { _ = d.ReadBlock(ctx, 0x94, dst) },
	} {
		if n := testing.AllocsPerRun(200, fn); n != 0 {
			t.Errorf("%s allocates %v times per call, want 0", name, n)
		}
	}
}
