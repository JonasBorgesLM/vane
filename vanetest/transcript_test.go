package vanetest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// fakeT collects what a test would report, so a test can assert that a
// divergence is reported.
type fakeT struct {
	errs     []string
	cleanups []func()
}

func (f *fakeT) Helper() {}
func (f *fakeT) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}
func (f *fakeT) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }
func (f *fakeT) finish() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

const header = "# vane transcript v1\n"

func replay(t vanetest.T, body string) *vanetest.ReplayBus {
	return vanetest.NewReplayBus(t, strings.NewReader(header+body))
}

// session is the traffic of a driver starting up: identify, configure, read.
// The same function runs over the recording bus and over the replay.
func session(b vane.Bus) ([]byte, error) {
	out := make([]byte, 5)
	id, data := out[:1], out[1:]
	if err := b.Tx(ctx, 0x5F, []byte{0x0F}, id); err != nil {
		return nil, err
	}
	if err := b.Tx(ctx, 0x5F, []byte{0x20, 0x85}, nil); err != nil {
		return nil, err
	}
	if err := b.Tx(ctx, 0x5F, []byte{0x28 | 0x80}, data); err != nil {
		return nil, err
	}
	return out, nil
}

func TestRecordedSessionReplaysByteForByte(t *testing.T) {
	c := newChip(0x5F)
	for i := uint8(0); i < 4; i++ {
		c.Set(0x28+i, 0x10+i)
	}
	var transcript bytes.Buffer
	rec := vanetest.NewRecorder(vanetest.NewFakeBus(c), &transcript)
	want, err := session(rec)
	if err != nil {
		t.Fatal(err)
	}

	ft := &fakeT{}
	rb := vanetest.NewReplayBus(ft, bytes.NewReader(transcript.Bytes()))
	got, err := session(rb)
	ft.finish()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("replay = %x, %v; recording saw %x", got, err, want)
	}
	if len(ft.errs) != 0 || rb.Remaining() != 0 {
		t.Fatalf("a faithful replay reported %v, %d remaining", ft.errs, rb.Remaining())
	}
}

func TestRecorderWritesTheFormatTheArchitectureDocuments(t *testing.T) {
	c := newChip(0x5F)
	var out bytes.Buffer
	rec := vanetest.NewRecorder(vanetest.NewFakeBus(c), &out)
	rec.Comment("bus=%s board=%q", "/dev/i2c-1", "Sense HAT")
	if _, err := session(rec); err != nil {
		t.Fatal(err)
	}
	_ = rec.Tx(ctx, 0x1C, []byte{0x0F}, make([]byte, 1)) // nothing there: a NACK
	want := header +
		"# bus=/dev/i2c-1 board=\"Sense HAT\"\n" +
		"5f W 0f R bc\n" +
		"5f W 20 85\n" +
		"5f W a8 R 00 00 00 00\n" +
		"1c W 0f E nack\n"
	if out.String() != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", out.String(), want)
	}
	if rec.Err() != nil {
		t.Fatal(rec.Err())
	}
}

func TestACancelledTransactionIsNotRecorded(t *testing.T) {
	var out bytes.Buffer
	bus := vanetest.NewFakeBus(newChip(0x5F))
	bus.Inject(0x5F, vanetest.Latency(1<<62))
	rec := vanetest.NewRecorder(bus, &out)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := rec.Tx(cctx, 0x5F, []byte{0x0F}, make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if out.String() != header {
		t.Fatalf("a transaction that may not have reached the device was recorded: %q", out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRecorderKeepsTheFirstWriteError(t *testing.T) {
	rec := vanetest.NewRecorder(vanetest.NewFakeBus(newChip(0x5F)), failingWriter{})
	_, _ = session(rec)
	if !errors.Is(rec.Err(), io.ErrClosedPipe) {
		t.Fatalf("Err() = %v", rec.Err())
	}
}

func TestAReorderedTranscriptFails(t *testing.T) {
	ft := &fakeT{}
	rb := replay(ft, "5f W 20 85\n5f W 0f R bc\n") // the two lines of a session, swapped
	_, err := session(rb)
	var div *vanetest.DivergenceError
	if !errors.As(err, &div) || div.Index != 0 {
		t.Fatalf("error = %v, want a DivergenceError at transaction 0", err)
	}
	if len(ft.errs) != 1 || !strings.Contains(ft.errs[0], "got:  5f W 0f") || !strings.Contains(ft.errs[0], "want: 5f W 20 85") {
		t.Fatalf("reported %q, want both lines", ft.errs)
	}
}

// Two reads of the same shape swapped: only the bytes written tell them apart,
// so this is the case that fails when the comparison ignores them.
func TestAReorderedTranscriptFailsOnTheBytesAlone(t *testing.T) {
	ft := &fakeT{}
	rb := replay(ft, "5f W 10 R ee\n5f W 0f R bc\n")
	err := rb.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1))
	var div *vanetest.DivergenceError
	if !errors.As(err, &div) || div.Index != 0 || len(ft.errs) != 1 {
		t.Fatalf("error = %v, reported %v", err, ft.errs)
	}
}

func TestDivergenceIsStickySoTheFirstOneIsTheOneRead(t *testing.T) {
	ft := &fakeT{}
	rb := replay(ft, "5f W 20 85\n5f W 0f R bc\n")
	first := rb.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1))
	second := rb.Tx(ctx, 0x5F, []byte{0x20, 0x85}, nil) // would have matched line 0
	if first == nil || !errors.Is(second, first) || len(ft.errs) != 1 {
		t.Fatalf("first=%v second=%v reported=%d, want the same error and one report", first, second, len(ft.errs))
	}
}

func TestEachKindOfDivergence(t *testing.T) {
	tests := []struct {
		name string
		call func(b vane.Bus) error
	}{
		{"wrong address", func(b vane.Bus) error { return b.Tx(ctx, 0x5C, []byte{0x0F}, make([]byte, 1)) }},
		{"wrong bytes written", func(b vane.Bus) error { return b.Tx(ctx, 0x5F, []byte{0x0E}, make([]byte, 1)) }},
		{"wrong read length", func(b vane.Bus) error { return b.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 2)) }},
		{"read where the transcript wrote", func(b vane.Bus) error { return b.Tx(ctx, 0x5F, []byte{0x0F}, nil) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeT{}
			err := tc.call(replay(ft, "5f W 0f R bc\n"))
			var div *vanetest.DivergenceError
			if !errors.As(err, &div) || len(ft.errs) != 1 {
				t.Fatalf("error = %v, reported %v", err, ft.errs)
			}
		})
	}
}

func TestTransactionsPastTheEndDiverge(t *testing.T) {
	ft := &fakeT{}
	rb := replay(ft, "5f W 0f R bc\n")
	if _, err := session(rb); err == nil {
		t.Fatal("a session longer than the transcript succeeded")
	}
	if len(ft.errs) != 1 {
		t.Fatalf("reported %v", ft.errs)
	}
}

func TestStoppingEarlyIsReportedWhenTheTestEnds(t *testing.T) {
	ft := &fakeT{}
	rb := replay(ft, "5f W 0f R bc\n5f W 20 85\n")
	if err := rb.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if len(ft.errs) != 0 {
		t.Fatalf("reported too early: %v", ft.errs)
	}
	ft.finish()
	if len(ft.errs) != 1 || !strings.Contains(ft.errs[0], "1 of 2") {
		t.Fatalf("reported %v, want one unreplayed-transcript report", ft.errs)
	}
}

func TestRecordedErrorsComeBack(t *testing.T) {
	tests := []struct {
		kind string
		want error
	}{
		{"nack", vane.ErrNotPresent},
		{"eio", vanetest.ErrIO},
		{"short", vanetest.ErrShortRead},
		{"error", vanetest.ErrRecordedError},
	}
	for _, tc := range tests {
		ft := &fakeT{}
		rb := replay(ft, "5f W 0f E "+tc.kind+"\n")
		err := rb.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1))
		ft.finish()
		if !errors.Is(err, tc.want) || len(ft.errs) != 0 {
			t.Errorf("%s: error = %v, reported %v", tc.kind, err, ft.errs)
		}
	}
}

func TestEveryErrorKindSurvivesARoundTrip(t *testing.T) {
	for name, fault := range map[string]vanetest.Fault{
		"nack": vanetest.NACK(), "eio": vanetest.EIO(), "short": vanetest.ShortRead(),
	} {
		var out bytes.Buffer
		bus := vanetest.NewFakeBus(newChip(0x5F))
		bus.Inject(0x5F, fault)
		recErr := vanetest.NewRecorder(bus, &out).Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1))

		ft := &fakeT{}
		repErr := vanetest.NewReplayBus(ft, &out).Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1))
		ft.finish()
		if recErr == nil || !errors.Is(repErr, recErr) && !errors.Is(recErr, repErr) {
			t.Errorf("%s: recorded %v, replayed %v", name, recErr, repErr)
		}
		if len(ft.errs) != 0 {
			t.Errorf("%s: %v", name, ft.errs)
		}
	}
}

func TestCommentsBlankLinesAndTrailingNotesAreIgnored(t *testing.T) {
	ft := &fakeT{}
	rb := vanetest.NewReplayBus(ft, strings.NewReader("\n"+header+"\n# bus=/dev/i2c-1 recorded=2026-10-20\n\n5f W 0f R bc   # HTS221 WHO_AM_I\n"))
	if err := rb.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	ft.finish()
	if len(ft.errs) != 0 {
		t.Fatalf("reported %v", ft.errs)
	}
}

func TestAMalformedTranscriptIsReportedAndEveryTransactionFails(t *testing.T) {
	tests := []struct{ name, text, want string }{
		{"no header", "5f W 0f R bc\n", "expected the header"},
		{"wrong version", "# vane transcript v2\n", "expected the header"},
		{"empty", "", "empty transcript"},
		{"bad address", header + "zz W 0f\n", "bad address"},
		{"bad byte", header + "5f W 0g\n", "bad byte"},
		{"one-digit byte", header + "5f W f\n", "two hex digits"},
		{"R before W", header + "5f R bc W 0f\n", "out of order"},
		{"R and E together", header + "5f W 0f R bc E nack\n", "out of order"},
		{"bytes before W", header + "5f 0f\n", "before W, R or E"},
		{"E without a kind", header + "5f W 0f E\n", "without an error kind"},
		{"only an address", header + "5f\n", "no bytes and no error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeT{}
			rb := vanetest.NewReplayBus(ft, strings.NewReader(tc.text))
			if len(ft.errs) != 1 || !strings.Contains(ft.errs[0], tc.want) {
				t.Fatalf("reported %q, want %q", ft.errs, tc.want)
			}
			if err := rb.Tx(ctx, 0x5F, []byte{0x0F}, make([]byte, 1)); err == nil {
				t.Fatal("a bus built from a malformed transcript answered")
			}
		})
	}
}

func TestACancelledContextNeverAdvancesTheReplay(t *testing.T) {
	ft := &fakeT{}
	rb := replay(ft, "5f W 0f R bc\n")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := rb.Tx(cctx, 0x5F, []byte{0x0F}, make([]byte, 1)); !errors.Is(err, context.Canceled) || rb.Remaining() != 1 {
		t.Fatalf("error = %v, remaining = %d", err, rb.Remaining())
	}
}
