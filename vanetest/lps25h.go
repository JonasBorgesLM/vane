package vanetest

import (
	"math"

	"github.com/JonasBorgesLM/vane"
)

// The LPS25H/LPS25HB register map, written from the sources in
// docs/hardware.md. Like the HTS221 model's, these numbers are repeated here
// and not shared with chip/lps25h.
const (
	lps25hAddr = 0x5C

	lRegWhoAmI  = 0x0F
	lRegResConf = 0x10
	lRegCtrl1   = 0x20
	lRegCtrl2   = 0x21
	lRegStatus  = 0x27
	lRegPressXL = 0x28 // PRESS_OUT_XL, _L, _H
	lRegTempL   = 0x2B // TEMP_OUT_L, _H

	lCtrl1PD        = 1 << 7
	lCtrl1ODRMask   = 0x70
	lCtrl1BDU       = 1 << 2
	lCtrl1NotVane   = 1<<0 | 1<<1 | 1<<3 // SIM, RESET_AZ, DIFF_EN
	lCtrl1MaxODR    = 4                  // 25 Hz; 5 to 7 are reserved
	lCtrl2OneShot   = 1 << 0
	lStatusTDA      = 1 << 0
	lStatusPDA      = 1 << 1
	lStatusTOR      = 1 << 4
	lStatusPOR      = 1 << 5
	lResConfDefault = 0x05
)

// LPS25HModel is a register-level model of the LPS25H or LPS25HB on a FakeBus.
//
// The writable set is the two control registers vane may touch, CTRL_REG1 and
// CTRL_REG2. RES_CONF, CTRL_REG3 and 4, the reference, threshold and FIFO
// registers are writable on the chip, but vane has no reason to write them
// (averaging is deliberately left alone, see docs/hardware.md), so a write to
// one is a driver bug and fails the test.
//
// Two further rules are vane's, not the chip's, and are reported: CTRL_REG1 is
// never written with BDU off (ADR-0005) or with SIM, RESET_AZ or DIFF_EN set,
// since differential mode would change what the pressure registers mean; and a
// one-shot trigger that arrives while another conversion is in flight or its
// sample is unread is an overlapping sequence (ADR-0004).
type LPS25HModel struct {
	regs *Regs
	opts modelOptions

	p vane.Hectopascal
	t vane.Celsius

	// readOut records which of the five output bytes were read since the last
	// sample was latched: bits 0 to 2 pressure, bits 3 and 4 temperature.
	readOut     uint8
	converting  bool
	presPending bool // staggered: the pressure half is still to finish
	pending     int
	stalled     bool
}

var (
	_ Device       = (*LPS25HModel)(nil)
	_ Resetter     = (*LPS25HModel)(nil)
	_ ReadyStaller = (*LPS25HModel)(nil)
)

// NewLPS25H returns a model at the LPS25H's address 0x5C, in its power-on
// state: power-down, no sample.
func NewLPS25H(opts ...ModelOption) *LPS25HModel {
	m := &LPS25HModel{opts: newModelOptions(opts)}
	m.regs = NewRegs(m.opts.rep, lps25hAddr, lRegCtrl1, lRegCtrl2)
	id := uint8(0xBD)
	if m.opts.whoAmI != nil {
		id = *m.opts.whoAmI
	}
	m.regs.Set(lRegWhoAmI, id)
	m.powerOn()
	return m
}

// Addr implements Device.
func (m *LPS25HModel) Addr() uint16 { return m.regs.Addr() }

// Peek returns a register's value, bypassing the bus.
func (m *LPS25HModel) Peek(reg uint8) uint8 { return m.regs.Get(reg) }

// Poke stores a value in a register, bypassing the bus and the writable set. It
// is how a test makes the chip raise an overrun bit.
func (m *LPS25HModel) Poke(reg, val uint8) { m.regs.Set(reg, val) }

// Set sets the pressure and temperature the chip measures. The output registers
// take them when a conversion completes. The counts are the inverse of the
// datasheet's conversion: 4096 per hPa, and 480 per degree around 42.5 °C.
func (m *LPS25HModel) Set(p vane.Hectopascal, t vane.Celsius) { m.p, m.t = p, t }

// NewSample makes the chip finish a conversion, as it does on its own every
// period in continuous mode. It does nothing in power-down, in one-shot mode or
// while the data-ready bits are stalled.
func (m *LPS25HModel) NewSample() {
	c1 := m.regs.Get(lRegCtrl1)
	if c1&lCtrl1PD != 0 && c1&lCtrl1ODRMask != 0 {
		m.complete()
	}
}

// Reset gives the chip the state of a power-on or a brown-out.
func (m *LPS25HModel) Reset() { m.powerOn() }

// StallReady holds the data-ready bits low, as for a chip that stopped
// converting.
func (m *LPS25HModel) StallReady(stalled bool) { m.stalled = stalled }

func (m *LPS25HModel) powerOn() {
	for _, reg := range []uint8{lRegCtrl1, lRegCtrl2, lRegCtrl2 + 1, lRegCtrl2 + 2, lRegStatus,
		lRegPressXL, lRegPressXL + 1, lRegPressXL + 2, lRegTempL, lRegTempL + 1} {
		m.regs.Set(reg, 0)
	}
	m.regs.Set(lRegResConf, lResConfDefault)
	m.converting, m.presPending, m.pending, m.readOut = false, false, 0, 0
}

// Tx implements Device.
func (m *LPS25HModel) Tx(w, rd []byte) error {
	if len(w) > 0 && len(rd) == 0 {
		if err := m.checkWrite(w); err != nil {
			return err
		}
	}
	if len(w) == 1 && len(rd) > 0 {
		m.beforeRead(int(w[0]&regMask), len(rd))
	}
	if err := m.regs.Tx(w, rd); err != nil {
		return err
	}
	if len(rd) == 0 {
		m.afterWrite(w)
	} else {
		m.afterRead(int(w[0]&regMask), len(rd))
	}
	return nil
}

// checkWrite enforces the rules that are vane's.
func (m *LPS25HModel) checkWrite(w []byte) error {
	reg := int(w[0] & regMask)
	data := w[1:]
	if i := lRegCtrl1 - reg; i >= 0 && i < len(data) {
		v := data[i]
		switch {
		case v&lCtrl1BDU == 0:
			return m.regs.violation("CTRL_REG1 written as 0x%02x with BDU = 0: block data update must stay on (ADR-0005)", v)
		case v&lCtrl1NotVane != 0:
			return m.regs.violation("CTRL_REG1 written as 0x%02x with SIM, RESET_AZ or DIFF_EN set: vane never enables 3-wire SPI, the autozero reset or differential mode", v)
		case (v&lCtrl1ODRMask)>>4 > lCtrl1MaxODR:
			return m.regs.violation("CTRL_REG1 written as 0x%02x with a reserved ODR", v)
		}
	}
	if i := lRegCtrl2 - reg; i >= 0 && i < len(data) {
		v := data[i]
		if v&^lCtrl2OneShot != 0 {
			return m.regs.violation("CTRL_REG2 written as 0x%02x: vane sets only ONE_SHOT, never AUTOZERO, SWRESET, the FIFO bits or BOOT", v)
		}
		if v&lCtrl2OneShot != 0 && m.inOneShotMode() &&
			(m.converting || m.regs.Get(lRegStatus)&(lStatusPDA|lStatusTDA) != 0) {
			return m.regs.violation("ONE_SHOT written while another one-shot sequence is unfinished: trigger, wait and read must not interleave with another sequence on the same chip (ADR-0004)")
		}
	}
	return nil
}

func (m *LPS25HModel) inOneShotMode() bool {
	c1 := m.regs.Get(lRegCtrl1)
	return c1&lCtrl1PD != 0 && c1&lCtrl1ODRMask == 0
}

// beforeRead lets a conversion complete when the status register is read, after
// as many "not ready" answers as the model was asked for.
func (m *LPS25HModel) beforeRead(reg, n int) {
	if reg > lRegStatus || reg+n <= lRegStatus {
		return
	}
	if m.presPending && !m.stalled {
		m.latchPressure()
		m.presPending = false
	}
	if !m.converting {
		return
	}
	if m.pending > 0 {
		m.pending--
		return
	}
	m.complete()
}

// afterRead clears a quantity's data-ready and overrun bits once all of its
// output bytes have been read, in one transaction or several.
func (m *LPS25HModel) afterRead(reg, n int) {
	for i := range 5 {
		if r := lRegPressXL + i; r >= reg && r < reg+n {
			m.readOut |= 1 << i
		}
	}
	status := m.regs.Get(lRegStatus)
	if m.readOut&0b00111 == 0b00111 {
		status &^= lStatusPDA | lStatusPOR
		m.readOut &^= 0b00111
	}
	if m.readOut&0b11000 == 0b11000 {
		status &^= lStatusTDA | lStatusTOR
		m.readOut &^= 0b11000
	}
	m.regs.Set(lRegStatus, status)
}

func (m *LPS25HModel) afterWrite(w []byte) {
	reg := int(w[0] & regMask)
	for i := range w[1:] {
		switch reg + i {
		case lRegCtrl1:
			if m.regs.Get(lRegCtrl1)&lCtrl1PD == 0 {
				m.converting, m.presPending = false, false
			} else {
				m.startOneShot() // a ONE_SHOT latched while powered down runs now
			}
		case lRegCtrl2:
			m.startOneShot()
		}
	}
}

func (m *LPS25HModel) startOneShot() {
	c2 := m.regs.Get(lRegCtrl2)
	if c2&lCtrl2OneShot == 0 || m.converting {
		return
	}
	if m.regs.Get(lRegCtrl1)&lCtrl1PD == 0 {
		// Observed on a Pi (docs/hardware.md): written while powered down, the bit
		// stays 1, nothing converts, and the conversion runs on power-up.
		return
	}
	if !m.inOneShotMode() {
		m.regs.Set(lRegCtrl2, c2&^lCtrl2OneShot)
		return
	}
	m.converting, m.pending = true, m.opts.polls
}

// complete finishes a conversion. With BDU on, a sample may not replace one that
// has not been read: the output registers keep the old one and the new one is
// lost. Whether the real chip raises its overrun bits in that case is not
// confirmed (docs/hardware.md), so the model does not.
func (m *LPS25HModel) complete() {
	if m.stalled {
		return
	}
	if c2 := m.regs.Get(lRegCtrl2); c2&lCtrl2OneShot != 0 {
		m.regs.Set(lRegCtrl2, c2&^lCtrl2OneShot)
	}
	m.converting = false

	status := m.regs.Get(lRegStatus)
	if m.regs.Get(lRegCtrl1)&lCtrl1BDU != 0 && status&(lStatusPDA|lStatusTDA) != 0 {
		return
	}
	setLE16(m.regs, lRegTempL, clampInt16((float64(m.t)-42.5)*480))
	m.regs.Set(lRegStatus, status|lStatusTDA)
	m.readOut = 0
	if m.opts.staggered {
		m.presPending = true
		return
	}
	m.latchPressure()
}

// latchPressure loads the pressure output and raises its data-ready bit.
func (m *LPS25HModel) latchPressure() {
	setLE24(m.regs, lRegPressXL, clampInt24(float64(m.p)*4096))
	m.regs.Set(lRegStatus, m.regs.Get(lRegStatus)|lStatusPDA)
}

// clampInt16 rounds v to the nearest integer, saturating at the ends of int16
// as the chip's output does.
func clampInt16(v float64) int16 {
	return int16(math.Max(math.MinInt16, math.Min(math.MaxInt16, math.Round(v))))
}

// clampInt24 rounds v to the nearest integer, saturating at the ends of a 24-bit
// two's complement value. The result is returned in an int32.
func clampInt24(v float64) int32 {
	return int32(math.Max(-(1 << 23), math.Min(1<<23-1, math.Round(v))))
}

// setLE24 stores v as the chip does: 24-bit two's complement, low byte first.
func setLE24(r *Regs, reg uint8, v int32) {
	u := uint32(v) // #nosec G115 -- reinterpreting two's complement is the point
	r.Set(reg, uint8(u&0xFF))
	r.Set(reg+1, uint8(u>>8&0xFF))
	r.Set(reg+2, uint8(u>>16&0xFF))
}

// LPS25HPressure and LPS25HTemperature convert plain numbers to the types Set
// takes, for tests that table their inputs as float64.
func LPS25HPressure(p float64) vane.Hectopascal { return vane.Hectopascal(p) }

// LPS25HTemperature converts a plain number to vane.Celsius.
func LPS25HTemperature(t float64) vane.Celsius { return vane.Celsius(t) }
