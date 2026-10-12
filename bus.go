package vane

import "context"

// Bus is the only hardware seam. Drivers talk to a Bus and never to a device
// file, so the same driver runs over the Linux backend, a register fake or a
// recorded transcript. One method keeps fakes and wrappers trivial.
//
// Implementations must be safe for concurrent use. Tx must check ctx before it
// starts and must not retry: retry policy belongs to the consumer.
type Bus interface {
	// Tx writes w to the device at addr and then reads len(r) bytes into r as
	// one combined transaction with a repeated start. A nil or empty w means no
	// write; a nil or empty r means no read.
	Tx(ctx context.Context, addr uint16, w, r []byte) error
}
