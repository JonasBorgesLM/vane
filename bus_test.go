package vane_test

import (
	"context"
	"sync"
	"testing"

	"github.com/JonasBorgesLM/vane"
)

// recordingBus is the smallest vane.Bus: it records the arguments it got.
type recordingBus struct {
	mu   sync.Mutex
	addr uint16
	w    []byte
}

func (b *recordingBus) Tx(_ context.Context, addr uint16, w, r []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addr, b.w = addr, append([]byte(nil), w...)
	for i := range r {
		r[i] = 0xBC
	}
	return nil
}

// Compile-time: the interface is one method, so any such type is a Bus.
var _ vane.Bus = (*recordingBus)(nil)

func TestBusTxReadsIntoCallerBuffer(t *testing.T) {
	var b vane.Bus = &recordingBus{}
	r := make([]byte, 1)
	if err := b.Tx(context.Background(), 0x5F, []byte{0x0F}, r); err != nil {
		t.Fatal(err)
	}
	if r[0] != 0xBC {
		t.Fatalf("read %#x, want 0xbc", r[0])
	}
}
