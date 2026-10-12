package vanetest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/JonasBorgesLM/vane"
)

// transcriptHeader opens every transcript. ReplayBus requires it, so a file of
// another kind is refused instead of being replayed as nonsense.
const transcriptHeader = "# vane transcript v1"

// ErrRecordedError is what a replayed transaction returns when the recording
// holds an error of a kind other than the named ones.
var ErrRecordedError = errors.New("vanetest: recorded error")

// record is one transaction in a transcript.
type record struct {
	addr uint16
	w    []byte
	r    []byte
	kind string // "" for success; otherwise "nack", "eio", "short" or "error"
}

// errorKind names an error the way a transcript spells it.
func errorKind(err error) string {
	switch {
	case errors.Is(err, vane.ErrNotPresent):
		return "nack"
	case errors.Is(err, ErrIO):
		return "eio"
	case errors.Is(err, ErrShortRead):
		return "short"
	default:
		return "error"
	}
}

// kindError is errorKind's inverse.
func kindError(kind string) error {
	switch kind {
	case "nack":
		return vane.ErrNotPresent
	case "eio":
		return ErrIO
	case "short":
		return ErrShortRead
	default:
		return ErrRecordedError
	}
}

// describeAttempt renders what the code under test tried: the bytes it wrote
// and how many it wanted to read, since the bytes it will read are not known.
func describeAttempt(addr uint16, w []byte, readLen int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%02x", addr)
	if len(w) > 0 {
		b.WriteString(" W")
		writeHex(&b, w)
	}
	if readLen > 0 {
		fmt.Fprintf(&b, " (reads %d)", readLen)
	}
	return b.String()
}

// format renders a record as one transcript line:
//
//	5f W 0f R bc          a write then a read
//	5f W 20 85            a write alone
//	5c W 0f E nack        a failed transaction
func (rc record) format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%02x", rc.addr)
	if len(rc.w) > 0 {
		b.WriteString(" W")
		writeHex(&b, rc.w)
	}
	switch {
	case rc.kind != "":
		b.WriteString(" E " + rc.kind)
	case len(rc.r) > 0:
		b.WriteString(" R")
		writeHex(&b, rc.r)
	}
	return b.String()
}

func writeHex(b *strings.Builder, p []byte) {
	for _, v := range p {
		fmt.Fprintf(b, " %02x", v)
	}
}

// parseRecord reads one transcript line. ok is false for a blank or comment
// line. Anything after a '#' is a comment.
func parseRecord(line string) (rc record, ok bool, err error) {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	f := strings.Fields(line)
	if len(f) == 0 {
		return record{}, false, nil
	}
	addr, perr := strconv.ParseUint(f[0], 16, 16)
	if perr != nil {
		return record{}, false, fmt.Errorf("bad address %q", f[0])
	}
	rc.addr = uint16(addr)

	// W comes first; R and E follow it and exclude each other, so they share a
	// rank and a second one is out of order.
	rank := map[string]int{"W": 1, "R": 2, "E": 2}
	section := ""
	for _, tok := range f[1:] {
		if r, isSection := rank[tok]; isSection {
			if section != "" && r <= rank[section] {
				return record{}, false, fmt.Errorf("%q out of order: W comes first, then R or E", tok)
			}
			section = tok
			continue
		}
		switch section {
		case "W", "R":
			v, perr := strconv.ParseUint(tok, 16, 8)
			if perr != nil || len(tok) != 2 {
				return record{}, false, fmt.Errorf("bad byte %q: two hex digits expected", tok)
			}
			if section == "W" {
				rc.w = append(rc.w, byte(v))
			} else {
				rc.r = append(rc.r, byte(v))
			}
		case "E":
			if rc.kind != "" {
				return record{}, false, fmt.Errorf("more than one error kind: %q after %q", tok, rc.kind)
			}
			rc.kind = tok
		default:
			return record{}, false, fmt.Errorf("%q before W, R or E", tok)
		}
	}
	if section == "E" && rc.kind == "" {
		return record{}, false, errors.New("the E marker is without an error kind")
	}
	if len(rc.w) == 0 && len(rc.r) == 0 && rc.kind == "" {
		return record{}, false, errors.New("a transaction with no bytes and no error")
	}
	return rc, true, nil
}
