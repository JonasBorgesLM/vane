package vanetest

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/JonasBorgesLM/vane"
)

// Device is a chip model on a FakeBus.
type Device interface {
	// Addr returns the 7-bit address the device answers to.
	Addr() uint16
	// Tx handles one transaction: w is what the host writes, rd what it reads.
	Tx(w, rd []byte) error
}

// Tx is one entry in a FakeBus's log.
type Tx struct {
	Addr uint16
	W    []byte
	R    []byte // what the host read; nil when the transaction failed
	Err  error
}

// FakeBus is a vane.Bus over chip models. It routes by address, serialises
// transactions as a real bus does, injects faults and logs every transaction.
// It is safe for concurrent use.
type FakeBus struct {
	mu     sync.Mutex
	devs   map[uint16]Device
	faults map[uint16][]*pending
	log    []Tx
}

type pending struct{ Fault }

var _ vane.Bus = (*FakeBus)(nil)

// NewFakeBus returns a bus with the given devices. When two share an address
// the last one wins.
func NewFakeBus(devs ...Device) *FakeBus {
	b := &FakeBus{devs: make(map[uint16]Device), faults: make(map[uint16][]*pending)}
	for _, d := range devs {
		b.devs[d.Addr()] = d
	}
	return b
}

// Inject arms a fault on the device at addr. Faults stack and each counts its
// own transactions.
func (b *FakeBus) Inject(addr uint16, f Fault) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.faults[addr] = append(b.faults[addr], &pending{Fault: f})
}

// Clear removes every fault on addr and releases a stalled data-ready bit.
func (b *FakeBus) Clear(addr uint16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.faults, addr)
	if s, ok := b.devs[addr].(ReadyStaller); ok {
		s.StallReady(false)
	}
}

// Log returns every transaction that reached the bus, in order. Transactions
// whose context had already ended are not in it.
func (b *FakeBus) Log() []Tx {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Tx, len(b.log))
	for i, t := range b.log {
		out[i] = Tx{Addr: t.Addr, W: bytes.Clone(t.W), R: bytes.Clone(t.R), Err: t.Err}
	}
	return out
}

// Writes returns the bytes written to addr, in order, for assertions such as
// "nothing was written before the identity register was read".
func (b *FakeBus) Writes(addr uint16) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out [][]byte
	for _, t := range b.log {
		if t.Addr == addr && len(t.W) > 0 {
			out = append(out, bytes.Clone(t.W))
		}
	}
	return out
}

// Tx implements vane.Bus.
func (b *FakeBus) Tx(ctx context.Context, addr uint16, w, r []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	entry := Tx{Addr: addr, W: append([]byte(nil), w...)}
	err := b.do(ctx, addr, w, r)
	entry.Err = err
	if err == nil {
		entry.R = append([]byte(nil), r...)
	}
	b.log = append(b.log, entry)
	return err
}

// do runs one transaction with b.mu held.
func (b *FakeBus) do(ctx context.Context, addr uint16, w, r []byte) error {
	active := b.activeFaults(addr)
	dev := b.devs[addr]

	for _, f := range active {
		if f.kind == kindLatency {
			if err := sleep(ctx, f.delay); err != nil {
				return err
			}
		}
	}
	for _, f := range active {
		if d, ok := dev.(Resetter); ok && f.kind == kindReset {
			d.Reset()
		}
		if d, ok := dev.(ReadyStaller); ok && f.kind == kindStallReady {
			d.StallReady(true)
		}
	}
	for _, f := range active {
		if f.kind == kindNACK {
			return vane.ErrNotPresent
		}
		if f.kind == kindEIO {
			return ErrIO
		}
	}
	if dev == nil {
		return vane.ErrNotPresent
	}
	if err := dev.Tx(w, r); err != nil {
		return err
	}
	for _, f := range active {
		if f.kind == kindShortRead {
			clear(r[len(r)/2:])
			return ErrShortRead
		}
	}
	return nil
}

// activeFaults returns the faults that apply to this transaction and advances
// their counters, dropping those that are used up.
func (b *FakeBus) activeFaults(addr uint16) []*pending {
	var active, keep []*pending
	for _, f := range b.faults[addr] {
		if f.after > 0 {
			f.after--
			keep = append(keep, f)
			continue
		}
		active = append(active, f)
		if f.times == 1 {
			continue // used up
		}
		if f.times > 1 {
			f.times--
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		delete(b.faults, addr)
	} else {
		b.faults[addr] = keep
	}
	return active
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
