package vanetest

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// Errors a fault injects. They are what a real backend would surface for the
// same condition, so a driver's handling of them is the handling it needs on
// hardware.
var (
	// ErrIO stands for the EIO a failing transfer reports.
	ErrIO = errors.New("vanetest: injected I/O error")
	// ErrShortRead stands for a transfer that returned fewer bytes than asked.
	ErrShortRead = fmt.Errorf("vanetest: injected short read: %w", io.ErrUnexpectedEOF)
)

type faultKind uint8

const (
	kindNACK faultKind = iota + 1
	kindEIO
	kindShortRead
	kindLatency
	kindStallReady
	kindReset
)

// Fault describes a failure for FakeBus.Inject. Build one with NACK, EIO,
// ShortRead, Latency, StallReady or Reset, then refine it with After and Times.
type Fault struct {
	kind  faultKind
	delay time.Duration
	after int // transactions to let through before the fault starts
	times int // transactions the fault affects once started; 0 means until cleared
}

// NACK makes the device not acknowledge: the transaction fails with
// vane.ErrNotPresent and the device never sees it.
func NACK() Fault { return Fault{kind: kindNACK} }

// EIO makes the transaction fail with ErrIO; the device never sees it.
func EIO() Fault { return Fault{kind: kindEIO} }

// ShortRead lets the device handle the transaction, then reports ErrShortRead
// with the second half of the read buffer zeroed, as if the transfer had been
// cut off. A driver that ignores the error reads garbage.
func ShortRead() Fault { return Fault{kind: kindShortRead} }

// Latency delays each affected transaction by d, honouring the context, with
// the bus held: nothing else uses it meanwhile, as on a real bus.
func Latency(d time.Duration) Fault { return Fault{kind: kindLatency, delay: d} }

// StallReady calls StallReady(true) on a device that implements ReadyStaller,
// so its data-ready bit never rises. It happens once, when the fault starts,
// and lasts until Clear. It stands for a chip that stopped converting.
func StallReady() Fault { return Fault{kind: kindStallReady, times: 1} }

// Reset calls Reset on a device that implements Resetter, once, when the fault
// starts: the chip loses its configuration without any bus error, as after a
// brown-out (G-11).
func Reset() Fault { return Fault{kind: kindReset, times: 1} }

// After lets n transactions to the device through before the fault starts.
func (f Fault) After(n int) Fault { f.after = n; return f }

// Times limits the fault to n transactions once started; 0 means until Clear.
// StallReady and Reset default to 1, because they are events.
func (f Fault) Times(n int) Fault { f.times = n; return f }

// Resetter is implemented by chip models that can lose their configuration.
type Resetter interface{ Reset() }

// ReadyStaller is implemented by chip models whose data-ready bit can be held
// low.
type ReadyStaller interface{ StallReady(stalled bool) }
