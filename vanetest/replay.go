package vanetest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/JonasBorgesLM/vane"
)

// T is the part of *testing.T that ReplayBus uses. Reporting a divergence
// through the test as well as returning it means a driver that swallows the
// error still fails.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	Cleanup(func())
}

// DivergenceError reports that the code under test did not do what the
// transcript recorded.
type DivergenceError struct {
	Index int    // zero-based position in the transcript
	Got   string // the transaction the code attempted, in transcript syntax
	Want  string // what the transcript holds there, or "" past its end
}

func (e *DivergenceError) Error() string {
	if e.Want == "" {
		return fmt.Sprintf("vanetest: transaction %d diverges from the transcript: got %q, but the transcript has ended", e.Index, e.Got)
	}
	return fmt.Sprintf("vanetest: transaction %d diverges from the transcript:\n  got:  %s\n  want: %s", e.Index, e.Got, e.Want)
}

// ReplayBus is a vane.Bus that answers from a transcript. Each transaction must
// match the next recorded one exactly — address, bytes written, length read —
// or the bus reports a DivergenceError and keeps failing every later
// transaction, so the first divergence is the one that is read (RF-11).
type ReplayBus struct {
	t    T
	mu   sync.Mutex
	recs []record
	pos  int
	err  error // sticky: a parse error or the first divergence
}

var _ vane.Bus = (*ReplayBus)(nil)

// NewReplayBus parses transcript. A malformed transcript is reported through t
// and every transaction on the returned bus fails with the parse error. When
// the test ends, a transcript not fully replayed is reported too: code that
// stops early is a divergence as much as code that goes off script.
func NewReplayBus(t T, transcript io.Reader) *ReplayBus {
	t.Helper()
	b := &ReplayBus{t: t}
	if err := b.parse(transcript); err != nil {
		b.err = err
		t.Errorf("%v", err)
		return b
	}
	t.Cleanup(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.err == nil && b.pos < len(b.recs) {
			t.Errorf("vanetest: the transcript was not fully replayed: %d of %d transactions, next is %s",
				b.pos, len(b.recs), b.recs[b.pos].format())
		}
	})
	return b
}

func (b *ReplayBus) parse(r io.Reader) error {
	sc := bufio.NewScanner(r)
	first := true
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if first {
			if line == "" {
				continue
			}
			if line != transcriptHeader {
				return fmt.Errorf("vanetest: transcript line %d: expected the header %q, got %q", n, transcriptHeader, line)
			}
			first = false
			continue
		}
		rc, ok, err := parseRecord(line)
		if err != nil {
			return fmt.Errorf("vanetest: transcript line %d: %w", n, err)
		}
		if ok {
			b.recs = append(b.recs, rc)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("vanetest: reading the transcript: %w", err)
	}
	if first {
		return fmt.Errorf("vanetest: empty transcript: expected the header %q", transcriptHeader)
	}
	return nil
}

// Remaining returns how many recorded transactions have not been replayed yet.
func (b *ReplayBus) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.recs) - b.pos
}

// Tx implements vane.Bus.
func (b *ReplayBus) Tx(ctx context.Context, addr uint16, w, r []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}

	got := describeAttempt(addr, w, len(r))
	if b.pos >= len(b.recs) {
		return b.diverge(got, record{}, false)
	}
	want := b.recs[b.pos]
	if addr != want.addr || !bytes.Equal(w, want.w) || (want.kind == "" && len(r) != len(want.r)) {
		return b.diverge(got, want, true)
	}
	b.pos++
	if want.kind != "" {
		return kindError(want.kind)
	}
	copy(r, want.r)
	return nil
}

func (b *ReplayBus) diverge(got string, want record, haveWant bool) error {
	e := &DivergenceError{Index: b.pos, Got: got}
	if haveWant {
		e.Want = want.format()
	}
	b.err = e
	b.t.Helper()
	b.t.Errorf("%v", e)
	return e
}
