package conformance

import (
	"bytes"
	"testing"
	"time"
)

// TCS3400 numbers, repeated from the datasheet v2-00 (DS000411), not taken
// from a driver or a model. Registers are 0x80-0xFF and bit 7 is part of the
// address, so the block read here sends the sub-address as it is.
const (
	tcsEnable  = 0x80
	tcsATime   = 0x81
	tcsControl = 0x8F
	tcsID      = 0x92
	tcsStatus  = 0x93
	tcsData    = 0x94
	tcsPONAEN  = 0x03
	tcsAVALID  = 0x01
)

type tcsDev struct{ dev }

func (d tcsDev) block(reg uint8, n int) []byte {
	d.t.Helper()
	r := make([]byte, n)
	if err := d.bus.Tx(bg, d.addr, []byte{reg}, r); err != nil {
		d.t.Fatalf("reading %d bytes from register 0x%02x of 0x%02x: %v", n, reg, d.addr, err)
	}
	return r
}

// TCS3400 checks the colour sensor at addr (0x39 on the board tested).
func TCS3400(t *testing.T, tg Target, addr uint16) {
	tg.prepare()
	d := tcsDev{dev{t: t, bus: tg.Bus, addr: addr}}
	reset := func() {
		d.write(tcsEnable, 0x00)
		d.write(tcsATime, 0xFF)
		d.write(tcsControl, 0x00)
	}
	t.Cleanup(reset)

	t.Run("identity", func(t *testing.T) {
		d.t = t
		id := d.read(tcsID)
		if id>>2 != 0b100100 {
			t.Errorf("ID = %#x, want bits 7:2 = 100100 (p. 20)", id)
		}
		observe(t, "ID %#x: VID %02b (00 TCS34001/5, 11 TCS34003/7)", id, id&0x03)
	})

	t.Run("a burst increments with nothing to set", func(t *testing.T) {
		d.t = t
		reset()
		one := []byte{d.read(0x80), d.read(0x81), d.read(0x82), d.read(0x83)}
		if burst := d.block(0x80, 4); !bytes.Equal(burst, one) {
			t.Errorf("4 bytes from 0x80 = %x, want the single reads %x", burst, one)
		}
		if !bytes.Equal(one, []byte{0x00, 0xFF, 0x00, 0xFF}) {
			t.Errorf("ENABLE, ATIME, 0x82, WTIME = %x, want the reset values 00 ff 00 ff", one)
		}
	})

	t.Run("one integration per AEN", func(t *testing.T) {
		d.t = t
		reset()
		d.write(tcsATime, 0xF6) // 10 cycles, 27.8 ms
		d.write(tcsEnable, tcsPONAEN)
		if st := d.read(tcsStatus); st&tcsAVALID != 0 {
			t.Errorf("STATUS = %#x right after AEN: AVALID set before a cycle", st)
		}
		if !d.waitFor(tg, tcsStatus, tcsAVALID) {
			t.Fatalf("no cycle completed within %v: STATUS = %#x", tg.timeout(), d.read(tcsStatus))
		}
		observe(t, "counts C R G B at 1x, 27.8 ms: %x", d.block(tcsData, 8))
		d.write(tcsEnable, 0x01)
		if st := d.read(tcsStatus); st&tcsAVALID != 0 {
			t.Errorf("STATUS = %#x with AEN off: AVALID still set", st)
		}
		d.write(tcsEnable, tcsPONAEN)
		if st := d.read(tcsStatus); st&tcsAVALID != 0 {
			t.Errorf("STATUS = %#x after AEN again: AVALID set before the new cycle", st)
		}
		observe(t, "STATUS reserved bits after AEN again: %#x", d.read(tcsStatus)&^tcsAVALID)
		tg.advance(40 * time.Millisecond)
	})
}
