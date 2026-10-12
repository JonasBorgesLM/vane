package vanetest_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// chip is the smallest model: a register file that records Reset and
// StallReady calls.
type chip struct {
	*vanetest.Regs
	resets  int
	stalled bool
}

func (c *chip) Reset()                  { c.resets++ }
func (c *chip) StallReady(stalled bool) { c.stalled = stalled }

func newChip(addr uint16) *chip {
	c := &chip{Regs: vanetest.NewRegs(nil, addr, 0x20)}
	c.Set(0x0F, 0xBC)
	for reg := uint8(0x10); reg < 0x14; reg++ {
		c.Set(reg, 0xEE) // never zero: a zeroed tail has to be the fake's doing
	}
	return c
}

var ctx = context.Background()

func readID(b vane.Bus, addr uint16) (byte, error) {
	r := make([]byte, 1)
	err := b.Tx(ctx, addr, []byte{0x0F}, r)
	return r[0], err
}

func TestRoutesByAddress(t *testing.T) {
	a, b := newChip(0x5F), newChip(0x5C)
	b.Set(0x0F, 0xBD)
	bus := vanetest.NewFakeBus(a, b)
	if got, err := readID(bus, 0x5F); err != nil || got != 0xBC {
		t.Errorf("0x5f = %#x, %v", got, err)
	}
	if got, err := readID(bus, 0x5C); err != nil || got != 0xBD {
		t.Errorf("0x5c = %#x, %v", got, err)
	}
}

func TestAnAbsentAddressIsNotAcknowledged(t *testing.T) {
	if _, err := readID(vanetest.NewFakeBus(newChip(0x5F)), 0x1C); !errors.Is(err, vane.ErrNotPresent) {
		t.Fatalf("error = %v, want vane.ErrNotPresent", err)
	}
}

func TestACancelledContextNeverReachesTheBus(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := bus.Tx(cctx, 0x5F, []byte{0x0F}, make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if n := len(bus.Log()); n != 0 {
		t.Fatalf("log has %d entries for a transaction that never started", n)
	}
}

func TestFaultsFire(t *testing.T) {
	tests := []struct {
		name  string
		fault vanetest.Fault
		want  error
		seen  bool // the device handled the transaction
	}{
		{"NACK", vanetest.NACK(), vane.ErrNotPresent, false},
		{"EIO", vanetest.EIO(), vanetest.ErrIO, false},
		{"ShortRead", vanetest.ShortRead(), io.ErrUnexpectedEOF, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus := vanetest.NewFakeBus(newChip(0x5F))
			bus.Inject(0x5F, tc.fault)
			r := make([]byte, 4)
			err := bus.Tx(ctx, 0x5F, []byte{0x0F | 0x80}, r)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.name == "ShortRead" && (r[0] != 0xBC || r[1] != 0xEE || r[2] != 0 || r[3] != 0) {
				t.Errorf("read = %#v, want the first half as the device sent it and the second half zeroed", r)
			}
			if got := bus.Log()[0]; got.Err == nil || (got.R != nil) {
				t.Errorf("log entry = %+v, want the error and no read data", got)
			}
		})
	}
}

func TestShortReadLetsTheDeviceRun(t *testing.T) {
	c := newChip(0x5F)
	bus := vanetest.NewFakeBus(c)
	bus.Inject(0x5F, vanetest.ShortRead())
	_ = bus.Tx(ctx, 0x5F, []byte{0x20, 0x85}, nil) // a write has no read half to cut
	if c.Get(0x20) != 0x85 {
		t.Fatal("the device did not see the transaction a short read cuts off")
	}
}

func TestLatencyDelaysAndCancellationInterruptsIt(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F))
	bus.Inject(0x5F, vanetest.Latency(40*time.Millisecond).Times(1))
	start := time.Now()
	if _, err := readID(bus, 0x5F); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Errorf("transaction took %v, want at least 40ms", d)
	}

	bus.Inject(0x5F, vanetest.Latency(time.Hour))
	cctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	start = time.Now()
	err := bus.Tx(cctx, 0x5F, []byte{0x0F}, make([]byte, 1))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("error = %v after %v, want the hour-long latency cut by the deadline", err, time.Since(start))
	}
}

func TestResetAndStallReachTheModelOnce(t *testing.T) {
	c := newChip(0x5F)
	bus := vanetest.NewFakeBus(c)
	bus.Inject(0x5F, vanetest.Reset())
	bus.Inject(0x5F, vanetest.StallReady())
	for range 3 {
		if _, err := readID(bus, 0x5F); err != nil {
			t.Fatal(err)
		}
	}
	if c.resets != 1 || !c.stalled {
		t.Fatalf("resets=%d stalled=%v, want exactly one reset and a stall", c.resets, c.stalled)
	}
	bus.Clear(0x5F)
	if c.stalled {
		t.Fatal("Clear did not release the stalled data-ready bit")
	}
}

func TestAfterAndTimesBoundAFault(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F))
	bus.Inject(0x5F, vanetest.NACK().After(2).Times(2))
	got := make([]bool, 0, 6) // true: the transaction failed
	for range 6 {
		_, err := readID(bus, 0x5F)
		got = append(got, err != nil)
	}
	want := []bool{false, false, true, true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("failures = %v, want %v", got, want)
		}
	}
}

func TestAFaultWithoutTimesLastsUntilCleared(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F))
	bus.Inject(0x5F, vanetest.EIO())
	for range 5 {
		if _, err := readID(bus, 0x5F); !errors.Is(err, vanetest.ErrIO) {
			t.Fatalf("error = %v, want ErrIO to persist", err)
		}
	}
	bus.Clear(0x5F)
	if _, err := readID(bus, 0x5F); err != nil {
		t.Fatalf("after Clear: %v", err)
	}
}

func TestFaultsOnOneAddressLeaveTheOthersAlone(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F), newChip(0x5C))
	bus.Inject(0x5F, vanetest.NACK())
	if _, err := readID(bus, 0x5C); err != nil {
		t.Fatalf("0x5c affected by a fault on 0x5f: %v", err)
	}
}

func TestLogAndWritesKeepOrder(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F))
	_, _ = readID(bus, 0x5F)
	_ = bus.Tx(ctx, 0x5F, []byte{0x20, 0x01}, nil)
	_ = bus.Tx(ctx, 0x5F, []byte{0x20, 0x02}, nil)

	log := bus.Log()
	if len(log) != 3 || log[0].R[0] != 0xBC || log[2].W[1] != 0x02 {
		t.Fatalf("log = %+v", log)
	}
	w := bus.Writes(0x5F)
	if len(w) != 3 || w[0][0] != 0x0F || w[1][1] != 0x01 || w[2][1] != 0x02 {
		t.Fatalf("writes = %v: the register-address phase of a read counts as a write, in order", w)
	}
	log[0].W[0] = 0xFF
	if bus.Log()[0].W[0] != 0x0F {
		t.Fatal("Log returned the bus's own buffers")
	}
}

func TestConcurrentTransactionsAreSerialisedAndAllLogged(t *testing.T) {
	bus := vanetest.NewFakeBus(newChip(0x5F), newChip(0x5C))
	const workers, each = 8, 50
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := uint16(0x5F)
			if w%2 == 1 {
				addr = 0x5C
			}
			for range each {
				if _, err := readID(bus, addr); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n := len(bus.Log()); n != workers*each {
		t.Fatalf("log has %d entries, want %d", n, workers*each)
	}
}
