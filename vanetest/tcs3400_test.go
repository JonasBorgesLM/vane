package vanetest_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

type tcs struct {
	t   *testing.T
	m   *vanetest.TCS3400Model
	rec *recorder
}

func newTCS(t *testing.T, opts ...vanetest.ModelOption) *tcs {
	rec := &recorder{}
	return &tcs{t: t, m: vanetest.NewTCS3400(append([]vanetest.ModelOption{vanetest.WithReporter(rec)}, opts...)...), rec: rec}
}

func (c *tcs) write(reg, v byte) {
	c.t.Helper()
	if err := c.m.Tx([]byte{reg, v}, nil); err != nil {
		c.t.Fatalf("write 0x%02x = %#x: %v", reg, v, err)
	}
}

func (c *tcs) read(reg byte, n int) []byte {
	c.t.Helper()
	r := make([]byte, n)
	if err := c.m.Tx([]byte{reg}, r); err != nil {
		c.t.Fatalf("read 0x%02x x%d: %v", reg, n, err)
	}
	return r
}

func (c *tcs) status() byte { return c.read(0x93, 1)[0] }

func counts(b []byte) [4]uint32 {
	var out [4]uint32
	for i := range out {
		out[i] = uint32(b[2*i]) | uint32(b[2*i+1])<<8
	}
	return out
}

// The board read these at power-on (docs/hardware.md).
func TestTCS3400PowerOnState(t *testing.T) {
	c := newTCS(t)
	if got := c.read(0x80, 4); !bytes.Equal(got, []byte{0x00, 0xFF, 0x00, 0xFF}) {
		t.Errorf("0x80..0x83 = %x, want 00 ff 00 ff", got)
	}
	if got := c.read(0x8C, 7); !bytes.Equal(got, []byte{0x00, 0x40, 0x00, 0x00, 0x00, 0x61, 0x90}) {
		t.Errorf("0x8C..0x92 = %x, want 00 40 00 00 00 61 90", got)
	}
	if c.m.Addr() != 0x39 {
		t.Errorf("address %#x, want 0x39", c.m.Addr())
	}
}

func TestTCS3400OtherVariant(t *testing.T) {
	c := newTCS(t, vanetest.WithModelAddress(0x29), vanetest.WithWhoAmI(0x93))
	if c.m.Addr() != 0x29 || c.read(0x92, 1)[0] != 0x93 {
		t.Fatalf("address %#x ID %#x, want 0x29 and 0x93", c.m.Addr(), c.read(0x92, 1)[0])
	}
}

func TestTCS3400RefusesSubAddressesBelow0x80(t *testing.T) {
	c := newTCS(t)
	if err := c.m.Tx([]byte{0x12}, make([]byte, 1)); !errors.Is(err, vanetest.ErrProtocol) {
		t.Fatalf("0x12: %v, want a protocol violation", err)
	}
	if len(c.rec.msgs) != 1 || !strings.Contains(c.rec.msgs[0], "below 0x80") {
		t.Errorf("reported %q, want the below-0x80 rule", c.rec.msgs)
	}
}

// On the board: AEN set cleared AVALID; it was set once a cycle completed;
// turning AEN off cleared it; turning it on again left it clear until the next
// cycle. Negative control: a model that keeps AVALID across AEN fails the
// second half.
func TestTCS3400OneCyclePerAEN(t *testing.T) {
	c := newTCS(t, vanetest.WithConversionPolls(2))
	c.write(0x81, 0xC0) // 178 ms: room for counts above the reset ATIME's 1024
	c.m.Set(120, 57, 66, 60)
	c.write(0x80, 0x03)
	for i := range 2 {
		if st := c.status(); st&0x01 != 0 {
			t.Fatalf("poll %d: AVALID before the cycle completed (%#x)", i, st)
		}
	}
	if st := c.status(); st&0x01 == 0 {
		t.Fatalf("AVALID not set after the cycle (%#x)", st)
	}
	if got := counts(c.read(0x94, 8)); got != [4]uint32{120, 57, 66, 60} {
		t.Errorf("counts %v, want 120 57 66 60", got)
	}
	c.write(0x80, 0x01)
	if st := c.status(); st&0x01 != 0 {
		t.Fatalf("AVALID still set with AEN off (%#x)", st)
	}
	c.m.Set(3215, 736, 1463, 1408)
	c.write(0x80, 0x03)
	if st := c.status(); st&0x01 != 0 {
		t.Fatalf("AVALID set before the second cycle (%#x)", st)
	}
	for c.status()&0x01 == 0 {
	}
	if got := counts(c.read(0x94, 8)); got != [4]uint32{3215, 736, 1463, 1408} {
		t.Errorf("second cycle counts %v", got)
	}
}

func TestTCS3400NoCycleWithoutPON(t *testing.T) {
	c := newTCS(t)
	c.write(0x80, 0x02) // AEN alone
	for range 5 {
		if st := c.status(); st&0x01 != 0 {
			t.Fatalf("a cycle completed without PON (%#x)", st)
		}
	}
}

// min(cycles x 1024, 65535), cycles = 256 - ATIME (datasheet p. 15).
func TestTCS3400CountsClipAtTheIntegrationMaximum(t *testing.T) {
	for atime, max := range map[byte]uint32{0xFF: 1024, 0xF6: 10240, 0xC0: 65535, 0x00: 65535} {
		c := newTCS(t)
		c.write(0x81, atime)
		if got := c.m.MaxCount(); got != max {
			t.Errorf("ATIME %#x: max %d, want %d", atime, got, max)
		}
		c.m.Set(max+1, 10, 10, max)
		c.write(0x80, 0x03)
		st := c.status()
		if got := counts(c.read(0x94, 8)); got[0] != max || got[3] != max || st&0x80 == 0 {
			t.Errorf("ATIME %#x: counts %v status %#x, want C clipped to %d and ASAT", atime, got, st, max)
		}
	}
}

func TestTCS3400RefusesWhatVaneNeverDoes(t *testing.T) {
	for name, w := range map[string][]byte{
		"WEN":          {0x80, 0x0B},
		"AIEN":         {0x80, 0x13},
		"CONTROL high": {0x8F, 0x10},
		"WTIME":        {0x83, 0x00},
		"IR channel":   {0xC0, 0x80},
		"CICLEAR":      {0xE6, 0x00},
		"ID":           {0x92, 0x00},
		"past 0xFF":    {0xFF, 0x00, 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			if err := newTCS(t).m.Tx(w, nil); !errors.Is(err, vanetest.ErrProtocol) {
				t.Fatalf("err = %v, want a protocol violation", err)
			}
		})
	}
}
