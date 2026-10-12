package vanetest

import (
	"math"

	"github.com/JonasBorgesLM/vane"
)

// The HTS221 register map, written from the datasheet as reproduced in
// docs/hardware.md. These are deliberately not shared with chip/hts221: a
// model that reuses the driver's constants agrees with the driver's mistakes.
const (
	hts221Addr = 0x5F

	hRegWhoAmI = 0x0F
	hRegAvConf = 0x10
	hRegCtrl1  = 0x20
	hRegCtrl2  = 0x21
	hRegCtrl3  = 0x22
	hRegStatus = 0x27
	hRegHumL   = 0x28 // HUMIDITY_OUT_L; _H follows
	hRegTempL  = 0x2A // TEMP_OUT_L; _H follows

	hCtrl1PD      = 1 << 7
	hCtrl1BDU     = 1 << 2
	hCtrl1ODRMask = 0x03 // 0 = one-shot
	hCtrl2OneShot = 1 << 0
	hStatusTDA    = 1 << 0
	hStatusHDA    = 1 << 1

	hAvConfDefault = 0x1B // AVGH = 3 (32 samples), AVGT = 3 (16 samples)
)

// HTS221Factory is the model's factory calibration, as the datasheet defines
// the points: relative humidity and temperature at two references, with the
// raw ADC count the chip reads at each.
type HTS221Factory struct {
	H0RH, H1RH     float64 // %RH
	T0DegC, T1DegC float64 // °C
	H0T0Out        int16
	H1T0Out        int16
	T0Out          int16
	T1Out          int16
}

// The model's factory points. They are not the chip's typical values, they are
// chosen so every register encoding is exercised: signed counts on both sides
// of zero, and a T1 of 80 °C = 640 / 8, which needs the two high bits kept in
// register 0x35.
var hts221Factory = HTS221Factory{
	H0RH: 20, H1RH: 80, T0DegC: 20, T1DegC: 80,
	H0T0Out: -500, H1T0Out: 7500, T0Out: -200, T1Out: 2200,
}

// HTS221Model is a register-level model of the HTS221 on a FakeBus.
//
// It reproduces what the driver depends on: the identity register, the
// calibration block, BDU, one-shot and continuous acquisition, the data-ready
// flags, the writable registers, and a chip that resets or stops converting.
// A one-shot sequence is trigger → wait → read, and it must be atomic per chip
// (ADR-0004). The model reports a trigger that arrives while another
// conversion is in flight or while its sample is still unread, which is what
// two goroutines sharing a sensor without its mutex would do.
type HTS221Model struct {
	regs *Regs
	opts modelOptions

	rh vane.RelativeHumidity
	t  vane.Celsius

	// readOut records which of the four output bytes were read since the last
	// sample was latched, one bit each from HUMIDITY_OUT_L up.
	readOut    uint8
	converting bool // a one-shot conversion is under way
	humPending bool // staggered: the humidity half of a conversion is still to finish
	pending    int  // status reads still to answer "not ready"
	stalled    bool
}

var (
	_ Device       = (*HTS221Model)(nil)
	_ Resetter     = (*HTS221Model)(nil)
	_ ReadyStaller = (*HTS221Model)(nil)
)

// NewHTS221 returns a model at the HTS221's address 0x5F, in its power-on
// state: power-down, default averaging, no sample.
func NewHTS221(opts ...ModelOption) *HTS221Model {
	m := &HTS221Model{opts: newModelOptions(opts)}
	m.regs = NewRegs(m.opts.rep, hts221Addr, hRegAvConf, hRegCtrl1, hRegCtrl2, hRegCtrl3)
	m.loadFactoryData()
	m.powerOn()
	return m
}

// Factory returns the calibration points the model carries.
func (m *HTS221Model) Factory() HTS221Factory { return hts221Factory }

// Addr implements Device.
func (m *HTS221Model) Addr() uint16 { return m.regs.Addr() }

// Peek returns a register's value, bypassing the bus.
func (m *HTS221Model) Peek(reg uint8) uint8 { return m.regs.Get(reg) }

// Poke stores a value in a register, bypassing the bus and the writable set.
// It is how a test corrupts the calibration.
func (m *HTS221Model) Poke(reg, val uint8) { m.regs.Set(reg, val) }

// Set sets the relative humidity and temperature the chip measures. The
// output registers take them when a conversion completes.
func (m *HTS221Model) Set(rh vane.RelativeHumidity, t vane.Celsius) { m.rh, m.t = rh, t }

// NewSample makes the chip finish a conversion, as it does on its own every
// period in continuous mode. It does nothing in power-down, in one-shot mode or
// while the data-ready bit is stalled.
func (m *HTS221Model) NewSample() {
	c1 := m.regs.Get(hRegCtrl1)
	if c1&hCtrl1PD != 0 && c1&hCtrl1ODRMask != 0 {
		m.complete()
	}
}

// Reset gives the chip the state of a power-on or a brown-out: power-down,
// default averaging, no sample. The factory calibration survives, as it lives
// in non-volatile memory.
func (m *HTS221Model) Reset() { m.powerOn() }

// StallReady holds the data-ready bits low, as for a chip that stopped
// converting.
func (m *HTS221Model) StallReady(stalled bool) { m.stalled = stalled }

func (m *HTS221Model) powerOn() {
	for _, reg := range []uint8{hRegCtrl1, hRegCtrl2, hRegCtrl3, hRegStatus, hRegHumL, hRegHumL + 1, hRegTempL, hRegTempL + 1} {
		m.regs.Set(reg, 0)
	}
	m.regs.Set(hRegAvConf, hAvConfDefault)
	m.converting, m.humPending, m.pending, m.readOut = false, false, 0, 0
}

func (m *HTS221Model) loadFactoryData() {
	f := hts221Factory
	m.regs.Set(hRegWhoAmI, 0xBC)
	if m.opts.whoAmI != nil {
		m.regs.Set(hRegWhoAmI, *m.opts.whoAmI)
	}
	t0, t1 := uint16(f.T0DegC*8), uint16(f.T1DegC*8)
	m.regs.Set(0x30, uint8(f.H0RH*2))
	m.regs.Set(0x31, uint8(f.H1RH*2))
	m.regs.Set(0x32, low8(t0))
	m.regs.Set(0x33, low8(t1))
	m.regs.Set(0x35, low8((t0>>8)&0x3|((t1>>8)&0x3)<<2))
	setLE16(m.regs, 0x36, f.H0T0Out)
	setLE16(m.regs, 0x3A, f.H1T0Out)
	setLE16(m.regs, 0x3C, f.T0Out)
	setLE16(m.regs, 0x3E, f.T1Out)
}

// setLE16 stores v as the chip does: two's complement, low byte first.
func setLE16(r *Regs, reg uint8, v int16) {
	u := uint16(v) // #nosec G115 -- reinterpreting two's complement is the point
	r.Set(reg, low8(u))
	r.Set(reg+1, low8(u>>8))
}

// low8 returns the low byte of v.
func low8(v uint16) uint8 { return uint8(v & 0xFF) }

// Tx implements Device.
func (m *HTS221Model) Tx(w, rd []byte) error {
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
	switch {
	case len(rd) == 0:
		m.afterWrite(w)
	default:
		m.afterRead(int(w[0]&regMask), len(rd))
	}
	return nil
}

// checkWrite enforces the one rule about a write that is vane's and not the
// chip's: block data update stays on in every configuration (ADR-0005). Without
// it a multi-byte read can combine the high byte of one sample with the low
// byte of the next.
func (m *HTS221Model) checkWrite(w []byte) error {
	reg := int(w[0] & regMask)
	data := w[1:]
	if i := hRegCtrl2 - reg; i >= 0 && i < len(data) && data[i]&hCtrl2OneShot != 0 && m.inOneShotMode() &&
		(m.converting || m.regs.Get(hRegStatus)&(hStatusHDA|hStatusTDA) != 0) {
		return m.regs.violation("ONE_SHOT written while another one-shot sequence is unfinished: trigger, wait and read must not interleave with another sequence on the same chip (ADR-0004)")
	}
	if i := hRegCtrl1 - reg; i >= 0 && i < len(data) && data[i]&hCtrl1BDU == 0 {
		return m.regs.violation("CTRL_REG1 written as 0x%02x with BDU = 0: block data update must stay on (ADR-0005)", data[i])
	}
	return nil
}

// beforeRead lets a conversion complete when the status register is read, after
// as many "not ready" answers as the model was asked for.
func (m *HTS221Model) beforeRead(reg, n int) {
	if reg > hRegStatus || reg+n <= hRegStatus {
		return
	}
	if m.humPending && !m.stalled {
		m.latchHumidity()
		m.humPending = false
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

// afterRead clears a data-ready flag once both bytes of its output have been
// read, in one transaction or two, which is what lets the next sample replace
// it under BDU.
func (m *HTS221Model) afterRead(reg, n int) {
	for i := range 4 {
		if r := hRegHumL + i; r >= reg && r < reg+n {
			m.readOut |= 1 << i
		}
	}
	status := m.regs.Get(hRegStatus)
	if m.readOut&0b0011 == 0b0011 {
		status &^= hStatusHDA
		m.readOut &^= 0b0011
	}
	if m.readOut&0b1100 == 0b1100 {
		status &^= hStatusTDA
		m.readOut &^= 0b1100
	}
	m.regs.Set(hRegStatus, status)
}

func (m *HTS221Model) afterWrite(w []byte) {
	reg := int(w[0] & regMask)
	for i := range w[1:] {
		switch reg + i {
		case hRegCtrl1:
			if m.regs.Get(hRegCtrl1)&hCtrl1PD == 0 {
				m.converting = false // power-down stops a conversion
			}
		case hRegCtrl2:
			m.startOneShot()
		}
	}
}

func (m *HTS221Model) inOneShotMode() bool {
	c1 := m.regs.Get(hRegCtrl1)
	return c1&hCtrl1PD != 0 && c1&hCtrl1ODRMask == 0
}

// startOneShot begins a conversion if the chip is able to: powered up and with
// ODR = 0. Otherwise the request is ignored and the bit does not stay set.
func (m *HTS221Model) startOneShot() {
	c2 := m.regs.Get(hRegCtrl2)
	if c2&hCtrl2OneShot == 0 || m.converting {
		return
	}
	if !m.inOneShotMode() {
		m.regs.Set(hRegCtrl2, c2&^hCtrl2OneShot)
		return
	}
	m.converting, m.pending = true, m.opts.polls
}

// complete finishes a conversion. With BDU on, a sample is not allowed to
// replace one that has not been read: the output registers keep the old one and
// the new one is lost.
func (m *HTS221Model) complete() {
	if m.stalled {
		return
	}
	if m.regs.Get(hRegCtrl2)&hCtrl2OneShot != 0 {
		m.regs.Set(hRegCtrl2, m.regs.Get(hRegCtrl2)&^hCtrl2OneShot)
	}
	m.converting = false

	status := m.regs.Get(hRegStatus)
	bdu := m.regs.Get(hRegCtrl1)&hCtrl1BDU != 0
	if bdu && status&(hStatusHDA|hStatusTDA) != 0 {
		return
	}
	f := hts221Factory
	setLE16(m.regs, hRegTempL, interpolate(float64(m.t), f.T0DegC, f.T1DegC, f.T0Out, f.T1Out))
	m.readOut = 0
	if m.opts.staggered {
		m.regs.Set(hRegStatus, status|hStatusTDA)
		m.humPending = true
		return
	}
	m.regs.Set(hRegStatus, status|hStatusTDA)
	m.latchHumidity()
}

// latchHumidity loads the humidity output and raises its data-ready bit.
func (m *HTS221Model) latchHumidity() {
	f := hts221Factory
	setLE16(m.regs, hRegHumL, interpolate(float64(m.rh), f.H0RH, f.H1RH, f.H0T0Out, f.H1T0Out))
	m.regs.Set(hRegStatus, m.regs.Get(hRegStatus)|hStatusHDA)
}

// interpolate is the inverse of the chip's conversion: the raw count at which a
// reading of v falls on the straight line through the two calibration points.
// It is the model's own arithmetic, written separately from the driver's.
func interpolate(v, v0, v1 float64, out0, out1 int16) int16 {
	raw := float64(out0) + (v-v0)*float64(int(out1)-int(out0))/(v1-v0)
	return int16(math.Max(math.MinInt16, math.Min(math.MaxInt16, math.Round(raw))))
}

// HTS221Humidity and HTS221Temperature convert plain numbers to the types Set
// takes, for tests that table their inputs as float64.
func HTS221Humidity(rh float64) vane.RelativeHumidity { return vane.RelativeHumidity(rh) }

// HTS221Temperature converts a plain number to vane.Celsius.
func HTS221Temperature(t float64) vane.Celsius { return vane.Celsius(t) }
