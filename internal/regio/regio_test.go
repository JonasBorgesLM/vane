package regio_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JonasBorgesLM/vane/internal/regio"
)

const addr = 0x5F

// stFake behaves like an ST sensor on the bus: a multi-byte read without the
// auto-increment bit is an error here, where a real chip would silently return
// the same register repeatedly.
type stFake struct {
	regs   [128]byte
	calls  int
	lastW  []byte
	failAt error
}

func (f *stFake) Tx(ctx context.Context, a uint16, w, r []byte) error {
	f.calls++
	f.lastW = append(f.lastW[:0], w...)
	if f.failAt != nil {
		return f.failAt
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a != addr {
		return errors.New("fake: wrong address")
	}
	switch {
	case len(w) == 2 && len(r) == 0:
		f.regs[w[0]] = w[1]
	case len(w) == 1 && len(r) == 1:
		if w[0]&0x80 != 0 {
			return errors.New("fake: auto-increment bit on a single read")
		}
		r[0] = f.regs[w[0]]
	case len(w) == 1 && len(r) > 1:
		if w[0]&0x80 == 0 {
			return errors.New("fake: multi-byte read without the auto-increment bit")
		}
		copy(r, f.regs[w[0]&0x7F:])
	default:
		return errors.New("fake: unexpected transaction shape")
	}
	return nil
}

func newFake() *stFake {
	f := &stFake{}
	for i := range f.regs {
		f.regs[i] = byte(i) ^ 0xA5
	}
	return f
}

func TestReadReturnsTheRegisterWithoutAutoIncrement(t *testing.T) {
	f := newFake()
	got, err := regio.New(f, addr).Read(context.Background(), 0x0F)
	if err != nil {
		t.Fatal(err)
	}
	if want := f.regs[0x0F]; got != want {
		t.Fatalf("Read(0x0f) = %#x, want %#x", got, want)
	}
	if len(f.lastW) != 1 || f.lastW[0] != 0x0F {
		t.Fatalf("wrote %#v, want the bare register [0x0f]", f.lastW)
	}
}

func TestWriteStoresTheValue(t *testing.T) {
	f := newFake()
	if err := regio.New(f, addr).Write(context.Background(), 0x20, 0x85); err != nil {
		t.Fatal(err)
	}
	if f.regs[0x20] != 0x85 {
		t.Fatalf("register 0x20 = %#x, want 0x85", f.regs[0x20])
	}
}

// Negative control, seen failing: removing `| autoIncrement` in ReadBlock makes
// this test fail with the fake's "without the auto-increment bit" error.
func TestReadBlockSetsAutoIncrementAndReadsConsecutiveRegisters(t *testing.T) {
	f := newFake()
	dst := make([]byte, 4)
	if err := regio.New(f, addr).ReadBlock(context.Background(), 0x28, dst); err != nil {
		t.Fatal(err)
	}
	for i, b := range dst {
		if want := f.regs[0x28+i]; b != want {
			t.Errorf("dst[%d] = %#x, want register 0x%02x = %#x", i, b, 0x28+i, want)
		}
	}
	if f.lastW[0] != 0x28|0x80 {
		t.Fatalf("sub-address = %#x, want 0xa8", f.lastW[0])
	}
}

func TestRegisterOutOfRangeIsRefusedBeforeTouchingTheBus(t *testing.T) {
	f := newFake()
	d := regio.New(f, addr)
	ctx := context.Background()
	if _, err := d.Read(ctx, 0x80); err == nil {
		t.Error("Read(0x80) succeeded")
	}
	if err := d.Write(ctx, 0xA8, 1); err == nil {
		t.Error("Write(0xa8) succeeded")
	}
	if err := d.ReadBlock(ctx, 0xA8, make([]byte, 2)); err == nil {
		t.Error("ReadBlock(0xa8) succeeded: bit 7 would be mistaken for the request")
	}
	if f.calls != 0 {
		t.Fatalf("bus was called %d times for out-of-range registers", f.calls)
	}
}

func TestEmptyBlockIsRefusedBeforeTouchingTheBus(t *testing.T) {
	f := newFake()
	if err := regio.New(f, addr).ReadBlock(context.Background(), 0x28, nil); err == nil {
		t.Fatal("ReadBlock with no destination succeeded")
	}
	if f.calls != 0 {
		t.Fatalf("bus was called %d times", f.calls)
	}
}

func TestBusErrorsAndCancellationReachTheCaller(t *testing.T) {
	boom := errors.New("boom")
	f := newFake()
	f.failAt = boom
	d := regio.New(f, addr)
	ctx := context.Background()
	if _, err := d.Read(ctx, 0x0F); !errors.Is(err, boom) {
		t.Errorf("Read error = %v, want boom", err)
	}
	if err := d.Write(ctx, 0x20, 1); !errors.Is(err, boom) {
		t.Errorf("Write error = %v, want boom", err)
	}
	if err := d.ReadBlock(ctx, 0x28, make([]byte, 2)); !errors.Is(err, boom) {
		t.Errorf("ReadBlock error = %v, want boom", err)
	}

	f.failAt = nil
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Read(cctx, 0x0F); !errors.Is(err, context.Canceled) {
		t.Errorf("Read with a cancelled context = %v, want context.Canceled", err)
	}
}

// RNF-08: no allocation per read. A Dev owns its scratch buffer; a buffer local
// to a function would escape through the Bus interface (measured: 2 allocs/op).
func TestReadsDoNotAllocate(t *testing.T) {
	d := regio.New(newFake(), addr)
	ctx := context.Background()
	dst := make([]byte, 4)
	for name, fn := range map[string]func(){
		"Read":      func() { _, _ = d.Read(ctx, 0x0F) },
		"Write":     func() { _ = d.Write(ctx, 0x20, 0x85) },
		"ReadBlock": func() { _ = d.ReadBlock(ctx, 0x28, dst) },
	} {
		if n := testing.AllocsPerRun(200, fn); n != 0 {
			t.Errorf("%s allocates %v times per call, want 0", name, n)
		}
	}
}
