package vanetest

import "fmt"

// TCS3400 numbers, from the datasheet v2-00 (DS000411) and the board
// (docs/hardware.md), repeated here and not taken from a driver (ADR-0007).
const (
	tcsAddr = 0x39

	tcsEnable  = 0x80 // AIEN 4, WEN 3, AEN 1, PON 0
	tcsATime   = 0x81
	tcsWTime   = 0x83
	tcsConfig  = 0x8D
	tcsControl = 0x8F // AGAIN 1:0
	tcsRevID   = 0x91
	tcsID      = 0x92
	tcsStatus  = 0x93 // ASAT 7, AINT 4, AVALID 0
	tcsData    = 0x94 // CDATAL .. BDATAH

	tcsPON    = 1 << 0
	tcsAEN    = 1 << 1
	tcsAVALID = 1 << 0
	tcsASAT   = 1 << 7
)

// TCS3400Model is an ams TCS3400 colour sensor on a FakeBus.
//
// What it enforces, from the datasheet and the board:
//
//   - Registers are 0x80 to 0xFF and bit 7 is part of the address: a
//     sub-address below 0x80 is refused (on the board 0x12 read 0x24, outside
//     the map). A burst always increments, with no bit to set.
//   - One integration cycle completes per assertion of AEN, with PON on: AVALID
//     is clear when AEN is turned on and is set once the cycle completes, and
//     turning AEN off clears it (board). Here a cycle completes after the
//     conversion polls of WithConversionPolls, counted on STATUS reads.
//   - A count is capped at min(cycles × 1024, 65535), cycles = 256 − ATIME
//     (p. 15); a value set above the cap is clipped and raises ASAT.
//
// And vane's own rules: only ENABLE (PON and AEN), ATIME and CONTROL (AGAIN)
// are written; the wait timer, interrupts and the IR channel are not used
// (ADR-0009).
type TCS3400Model struct {
	opts       modelOptions
	addr       uint16
	mem        [256]byte
	cur        [4]uint32 // C, R, G, B to sample
	converting bool
	pending    int
}

var _ Device = (*TCS3400Model)(nil)

// NewTCS3400 returns a TCS34001 at 0x39 at its power-on state. WithWhoAmI
// gives another ID (0x93 for a TCS34003/7), WithModelAddress another address.
func NewTCS3400(opts ...ModelOption) *TCS3400Model {
	m := &TCS3400Model{opts: newModelOptions(opts), addr: tcsAddr}
	if m.opts.addr != nil {
		m.addr = *m.opts.addr
	}
	m.Reset()
	return m
}

// Addr implements Device.
func (m *TCS3400Model) Addr() uint16 { return m.addr }

// Peek returns a register's value, bypassing the rules.
func (m *TCS3400Model) Peek(reg uint8) uint8 { return m.mem[reg] }

// Poke stores a register's value, bypassing the rules.
func (m *TCS3400Model) Poke(reg, val uint8) { m.mem[reg] = val }

// Reset returns the chip to its power-on state (datasheet register map,
// p. 13; REVID as the board read it).
func (m *TCS3400Model) Reset() {
	m.mem = [256]byte{}
	id := uint8(0x90)
	if m.opts.whoAmI != nil {
		id = *m.opts.whoAmI
	}
	m.mem[tcsATime], m.mem[tcsWTime], m.mem[tcsConfig] = 0xFF, 0xFF, 0x40
	m.mem[tcsRevID], m.mem[tcsID] = 0x61, id
	m.converting, m.pending = false, 0
}

// Set gives the counts the next cycle produces: clear, red, green, blue.
func (m *TCS3400Model) Set(c, r, g, b uint32) { m.cur = [4]uint32{c, r, g, b} }

// MaxCount is the highest count at the current ATIME.
func (m *TCS3400Model) MaxCount() uint32 {
	cycles := 256 - uint32(m.mem[tcsATime])
	return min(cycles*1024, 65535)
}

func (m *TCS3400Model) violation(format string, args ...any) error {
	msg := fmt.Sprintf("device 0x%02x: "+format, append([]any{m.addr}, args...)...)
	if m.opts.rep != nil {
		m.opts.rep.Errorf("%s", msg)
	}
	return fmt.Errorf("%w: %s", ErrProtocol, msg)
}

// Tx implements Device.
func (m *TCS3400Model) Tx(w, rd []byte) error {
	if len(w) == 0 {
		return m.violation("empty write: a transaction starts with a sub-address")
	}
	reg := int(w[0])
	if reg < 0x80 {
		return m.violation("sub-address 0x%02x is below 0x80, outside the register map (on the board 0x12 read 0x24): bit 7 is part of the address", reg)
	}
	if len(rd) == 0 {
		data := w[1:]
		if len(data) == 0 {
			return m.violation("write to register 0x%02x carries no data", reg)
		}
		if reg+len(data) > len(m.mem) {
			return m.violation("write of %d bytes at register 0x%02x runs past 0xFF", len(data), reg)
		}
		for i, v := range data {
			if err := m.checkWrite(reg+i, v); err != nil {
				return err
			}
		}
		for i, v := range data {
			m.write(reg+i, v)
		}
		return nil
	}
	if len(w) != 1 {
		return m.violation("a read sends only the sub-address, got %d bytes", len(w))
	}
	if reg+len(rd) > len(m.mem) {
		return m.violation("read of %d bytes at register 0x%02x runs past 0xFF", len(rd), reg)
	}
	for i := range rd {
		r := reg + i
		if r == tcsStatus {
			m.poll()
		}
		rd[i] = m.mem[r]
	}
	return nil
}

func (m *TCS3400Model) checkWrite(reg int, v uint8) error {
	switch reg {
	case tcsEnable:
		if v&^uint8(tcsPON|tcsAEN) != 0 {
			return m.violation("ENABLE written as 0x%02x: vane sets only PON and AEN, never the wait timer or interrupts", v)
		}
	case tcsATime:
	case tcsControl:
		if v&^uint8(0x03) != 0 {
			return m.violation("CONTROL written as 0x%02x: only AGAIN, bits 1:0, is writable", v)
		}
	default:
		return m.violation("write to register 0x%02x, which vane has no reason to write", reg)
	}
	return nil
}

func (m *TCS3400Model) write(reg int, v uint8) {
	old := m.mem[reg]
	m.mem[reg] = v
	if reg != tcsEnable {
		return
	}
	on := v&(tcsPON|tcsAEN) == tcsPON|tcsAEN
	wasOn := old&(tcsPON|tcsAEN) == tcsPON|tcsAEN
	switch {
	case on && !wasOn:
		m.mem[tcsStatus] &^= tcsAVALID | tcsASAT
		m.converting, m.pending = true, m.opts.polls
	case !on:
		m.mem[tcsStatus] &^= tcsAVALID // board: AEN off clears AVALID
		m.converting = false
	}
}

// poll stands for time passing while the host waits on STATUS.
func (m *TCS3400Model) poll() {
	if !m.converting {
		return
	}
	if m.pending > 0 {
		m.pending--
		return
	}
	m.converting = false
	limit := m.MaxCount()
	st := m.mem[tcsStatus] | tcsAVALID
	for i, v := range m.cur {
		if v > limit {
			v, st = limit, st|tcsASAT
		}
		m.mem[tcsData+2*i] = uint8(v)        // #nosec G115 -- low byte of a count <= 65535
		m.mem[tcsData+2*i+1] = uint8(v >> 8) // #nosec G115 -- high byte
	}
	m.mem[tcsStatus] = st
}
