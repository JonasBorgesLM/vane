package vanetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/JonasBorgesLM/vane"
)

// Recorder is a vane.Bus that passes every transaction to another bus and
// writes it to a transcript as one line, in the order the bus handled them.
// Recorded on the Pi, a transcript becomes a test that runs anywhere (RF-11).
type Recorder struct {
	mu    sync.Mutex
	inner vane.Bus
	w     io.Writer
	err   error
}

var _ vane.Bus = (*Recorder)(nil)

// NewRecorder returns a Recorder over inner that writes to w, starting with the
// transcript header.
func NewRecorder(inner vane.Bus, w io.Writer) *Recorder {
	r := &Recorder{inner: inner, w: w}
	r.write(transcriptHeader + "\n")
	return r
}

// Comment adds a '#' line to the transcript, for the metadata a reader needs:
// the bus, the board revision, the kernel, the date, which overlay was loaded.
func (r *Recorder) Comment(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " ")
	r.write("# " + text + "\n")
}

// Err returns the first error writing the transcript, if any.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Tx implements vane.Bus. The lock is held across the inner call so the
// transcript order is the bus order. A transaction cut short by the context is
// not recorded: it may or may not have reached the device, and a transcript
// has to be replayable.
func (r *Recorder) Tx(ctx context.Context, addr uint16, w, rd []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	err := r.inner.Tx(ctx, addr, w, rd)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	rc := record{addr: addr, w: w}
	if err != nil {
		rc.kind = errorKind(err)
	} else {
		rc.r = rd
	}
	r.write(rc.format() + "\n")
	return err
}

func (r *Recorder) write(s string) {
	if r.err != nil {
		return
	}
	_, r.err = io.WriteString(r.w, s)
}
