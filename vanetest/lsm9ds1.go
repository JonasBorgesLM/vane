package vanetest

// LSM9DS1 models, written from DocID025715 Rev 3 and the board (docs/hardware.md),
// importing nothing from the driver (ADR-0007). The chip is two I2C devices with
// separate register maps, so it is two models: the accelerometer and gyroscope
// at 0x6A, the magnetometer at 0x1C.

const (
	agAddr  = 0x6A
	magAddr = 0x1C

	agWhoAmI      = 0x0F
	agCtrl1G      = 0x10 // ODR_G 7:5, FS_G 4:3
	agCtrl4       = 0x1E
	agCtrl5XL     = 0x1F
	agCtrl6XL     = 0x20 // ODR_XL 7:5, FS_XL 4:3
	agCtrl8       = 0x22 // BOOT 7, BDU 6, H_LACTIVE 5, PP_OD 4, SIM 3, IF_ADD_INC 2, BLE 1, SW_RESET 0
	agCtrl9       = 0x23 // FIFO_EN 1, STOP_ON_FTH 0
	agStatus      = 0x27 // TDA 2, GDA 1, XLDA 0
	agOutG        = 0x18 // OUT_X_G .. OUT_Z_G, 0x18 to 0x1D
	agOutGLast    = 0x1D
	agOutXL       = 0x28 // OUT_X_XL .. OUT_Z_XL, 0x28 to 0x2D
	agOutXLLast   = 0x2D
	agFIFOCtrl    = 0x2E // FMODE 7:5, FTH 4:0
	agFIFOSrc     = 0x2F // FTH 7, OVRN 6, FSS 5:0
	agODRShift    = 5
	agBDU         = 1 << 6
	agIfAddInc    = 1 << 2
	agStatusXLDA  = 1 << 0
	agStatusGDA   = 1 << 1
	agFIFOEn      = 1 << 1
	agStopOnFTH   = 1 << 0
	agFIFODepth   = 32
	fmodeBypass   = 0
	fmodeFIFO     = 1
	fmodeContinue = 6

	magWhoAmI   = 0x0F
	magCtrl1    = 0x20
	magCtrl2    = 0x21 // REBOOT 3, SOFT_RST 2
	magCtrl3    = 0x22 // I2C_DISABLE 7, LP 5, SIM 2, MD 1:0
	magCtrl4    = 0x23
	magCtrl5    = 0x24 // FAST_READ 7, BDU 6
	magStatus   = 0x27 // ZYXOR 7, ZYXDA 3
	magOut      = 0x28 // OUT_X_L_M .. OUT_Z_H_M, 0x28 to 0x2D
	magOutLast  = 0x2D
	magBDU      = 1 << 6
	magZYXDA    = 1 << 3
	magZYXOR    = 1 << 7
	magMDMask   = 0x03
	magMDCont   = 0x00
	magMDSingle = 0x01
)

// Axes is one three-axis raw sample, in counts, little-endian on the chip.
type Axes [3]int16

type motion struct{ g, xl Axes }

// LSM9DS1AccelGyroModel is the accelerometer and gyroscope device of an LSM9DS1.
//
// What it enforces, from the datasheet and the board:
//
//   - The sub-address is the 7-bit register only. A sub-address with bit 7 set
//     is refused: on the board 0x8F read 0x00 instead of WHO_AM_I, so a driver
//     that sends the ST auto-increment bit here reads another register.
//   - A multi-byte read needs CTRL_REG8.IF_ADD_INC set; without it the chip
//     returns the same register repeatedly.
//   - A burst through the outputs walks OUT_X_G..OUT_Z_G, then OUT_X_XL..OUT_Z_XL,
//     then back to OUT_X_G, skipping the registers between them (board). With
//     the gyroscope off, OUT_Z_XL wraps to OUT_X_XL (datasheet p. 21).
//   - With the FIFO on (CTRL_REG9.FIFO_EN, FMODE not Bypass) the outputs show
//     the oldest unread sample, and reading OUT_Z_XL_H takes it out: on the
//     board a 12-byte burst lowered FSS by one, the six gyroscope bytes alone
//     did not, the six accelerometer bytes did.
//   - Continuous mode overwrites the oldest sample when full and sets OVRN;
//     FIFO mode stops at 32 and, as the board showed, also reports OVRN; taking
//     a sample out clears it. Writing Bypass empties the FIFO.
//
// And vane's own rules, reported as violations: CTRL_REG8 keeps BDU and
// IF_ADD_INC set and SIM, BLE, BOOT, SW_RESET clear; FMODE is Bypass, FIFO or
// Continuous; nothing outside the writable set (CTRL_REG1_G, CTRL_REG6_XL,
// CTRL_REG8, CTRL_REG9, FIFO_CTRL) is written.
//
// There is no clock: Tick produces one sample at the configured rate, as the
// chip does every ODR period.
type LSM9DS1AccelGyroModel struct {
	regs *Regs
	opts modelOptions

	cur  motion
	fifo []motion
	ovrn bool
	// GDA clears when the gyroscope outputs are read and XLDA when the
	// accelerometer's are: seen on the board by the conformance suite, with the
	// last byte of each block standing for "read" as on the HTS221 and LPS25H.
	// The chip also raises TDA (bit 2) for its temperature sensor, which this
	// model does not have.
}

var _ Device = (*LSM9DS1AccelGyroModel)(nil)

// NewLSM9DS1AccelGyro returns the accelerometer and gyroscope at 0x6A, at their
// power-on state.
func NewLSM9DS1AccelGyro(opts ...ModelOption) *LSM9DS1AccelGyroModel {
	m := &LSM9DS1AccelGyroModel{opts: newModelOptions(opts)}
	m.regs = NewRegs(m.opts.rep, agAddr, agCtrl1G, agCtrl6XL, agCtrl8, agCtrl9, agFIFOCtrl)
	m.Reset()
	return m
}

// Addr implements Device.
func (m *LSM9DS1AccelGyroModel) Addr() uint16 { return agAddr }

// Peek returns a register's value, bypassing the rules.
func (m *LSM9DS1AccelGyroModel) Peek(reg uint8) uint8 {
	if reg&regMask == agFIFOSrc {
		return m.fifoSrc()
	}
	return m.regs.Get(reg)
}

// Poke stores a register's value, bypassing the rules.
func (m *LSM9DS1AccelGyroModel) Poke(reg, val uint8) { m.regs.Set(reg, val) }

// Reset returns the device to its power-on state (datasheet Table 21, p. 39).
func (m *LSM9DS1AccelGyroModel) Reset() {
	for reg := range regCount {
		m.regs.Set(uint8(reg), 0) // #nosec G115 -- reg < 128
	}
	id := uint8(0x68)
	if m.opts.whoAmI != nil {
		id = *m.opts.whoAmI
	}
	m.regs.Set(agWhoAmI, id)
	m.regs.Set(agCtrl4, 0x38)
	m.regs.Set(agCtrl5XL, 0x38)
	m.regs.Set(agCtrl8, agIfAddInc)
	m.fifo, m.ovrn = m.fifo[:0], false
}

// Set gives the values the next Tick samples: gyroscope and accelerometer, in
// counts.
func (m *LSM9DS1AccelGyroModel) Set(g, xl Axes) { m.cur = motion{g: g, xl: xl} }

// Buffered returns how many samples the FIFO holds.
func (m *LSM9DS1AccelGyroModel) Buffered() int { return len(m.fifo) }

func (m *LSM9DS1AccelGyroModel) gyroOn() bool  { return m.regs.Get(agCtrl1G)>>agODRShift != 0 }
func (m *LSM9DS1AccelGyroModel) accelOn() bool { return m.regs.Get(agCtrl6XL)>>agODRShift != 0 }

func (m *LSM9DS1AccelGyroModel) fifoOn() bool {
	return m.regs.Get(agCtrl9)&agFIFOEn != 0 && m.regs.Get(agFIFOCtrl)>>5 != fmodeBypass
}

// Tick is one ODR period: with either sensor on, a new sample is taken, into the
// FIFO when it is on and into the output registers otherwise.
func (m *LSM9DS1AccelGyroModel) Tick() {
	if !m.gyroOn() && !m.accelOn() {
		return
	}
	if !m.fifoOn() {
		m.latch(m.cur)
		st := m.regs.Get(agStatus)
		if m.gyroOn() {
			st |= agStatusGDA
		}
		if m.accelOn() {
			st |= agStatusXLDA
		}
		m.regs.Set(agStatus, st)
		return
	}
	if len(m.fifo) == m.depth() {
		m.ovrn = true
		if m.regs.Get(agFIFOCtrl)>>5 == fmodeFIFO {
			return // FIFO mode stops collecting when full
		}
		m.fifo = append(m.fifo[:0], m.fifo[1:]...)
	}
	m.fifo = append(m.fifo, m.cur)
	m.latch(m.fifo[0])
}

func (m *LSM9DS1AccelGyroModel) depth() int {
	if m.regs.Get(agCtrl9)&agStopOnFTH != 0 {
		return int(m.regs.Get(agFIFOCtrl)&0x1F) + 1
	}
	return agFIFODepth
}

func (m *LSM9DS1AccelGyroModel) latch(s motion) {
	for i := range 3 {
		setLE16(m.regs, agOutG+uint8(2*i), s.g[i])   // #nosec G115 -- i < 3
		setLE16(m.regs, agOutXL+uint8(2*i), s.xl[i]) // #nosec G115 -- i < 3
	}
}

func (m *LSM9DS1AccelGyroModel) fifoSrc() uint8 {
	n := len(m.fifo)
	v := uint8(n) // #nosec G115 -- n <= 32
	if m.ovrn {
		v |= 1 << 6
	}
	if fth := int(m.regs.Get(agFIFOCtrl) & 0x1F); fth != 0 && n >= fth {
		v |= 1 << 7
	}
	return v
}

// next is the register a burst reads after reg (board, docs/hardware.md).
func (m *LSM9DS1AccelGyroModel) next(reg int) int {
	switch reg {
	case agOutGLast:
		return agOutXL
	case agOutXLLast:
		if m.gyroOn() {
			return agOutG
		}
		return agOutXL
	}
	return reg + 1
}

// Tx implements Device.
func (m *LSM9DS1AccelGyroModel) Tx(w, rd []byte) error {
	if len(w) == 0 {
		return m.regs.violation("empty write: a transaction starts with a sub-address")
	}
	if w[0]&regAutoIncrement != 0 {
		return m.regs.violation("sub-address 0x%02x has bit 7 set: on the accelerometer and gyroscope it addresses another register (on the board 0x8F read 0x00, not WHO_AM_I); auto-increment is CTRL_REG8.IF_ADD_INC", w[0])
	}
	reg := int(w[0])
	inc := m.regs.Get(agCtrl8)&agIfAddInc != 0
	if len(rd) == 0 {
		data := w[1:]
		switch {
		case len(data) == 0:
			return m.regs.violation("write to register 0x%02x carries no data", reg)
		case len(data) > 1 && !inc:
			return m.regs.violation("multi-byte write at register 0x%02x with CTRL_REG8.IF_ADD_INC clear", reg)
		case reg+len(data) > regCount:
			return m.regs.violation("write of %d bytes at register 0x%02x runs past the last register", len(data), reg)
		}
		for i, v := range data {
			if err := m.checkWrite(reg+i, v); err != nil {
				return err
			}
		}
		for i, v := range data {
			m.regs.Set(uint8(reg+i), v) // #nosec G115 -- bounded above
			m.afterWrite(reg + i)
		}
		return nil
	}
	if len(w) != 1 {
		return m.regs.violation("a read sends only the sub-address, got %d bytes", len(w))
	}
	if len(rd) > 1 && !inc {
		return m.regs.violation("multi-byte read at register 0x%02x with CTRL_REG8.IF_ADD_INC clear: the chip returns the same register repeatedly", reg)
	}
	r := reg
	for i := range rd {
		if r >= regCount {
			return m.regs.violation("read of %d bytes at register 0x%02x runs past the last register", len(rd), reg)
		}
		rd[i] = m.Peek(uint8(r)) // #nosec G115 -- r < 128
		m.afterRead(r)
		r = m.next(r)
	}
	return nil
}

func (m *LSM9DS1AccelGyroModel) checkWrite(reg int, v uint8) error {
	if !m.regs.writable[reg] {
		return m.regs.violation("write to register 0x%02x, which vane has no reason to write", reg)
	}
	switch reg {
	case agCtrl8:
		const allowed = agBDU | agIfAddInc | 1<<5 | 1<<4 // BDU, IF_ADD_INC, H_LACTIVE, PP_OD
		switch {
		case v&agBDU == 0:
			return m.regs.violation("CTRL_REG8 written as 0x%02x with BDU = 0: block data update must stay on (RF-06)", v)
		case v&agIfAddInc == 0:
			return m.regs.violation("CTRL_REG8 written as 0x%02x with IF_ADD_INC = 0: every block read would return one register repeated", v)
		case v&^uint8(allowed) != 0:
			return m.regs.violation("CTRL_REG8 written as 0x%02x: vane never sets BOOT, SIM, BLE or SW_RESET", v)
		}
	case agFIFOCtrl:
		switch v >> 5 {
		case fmodeBypass, fmodeFIFO, fmodeContinue:
		default:
			return m.regs.violation("FIFO_CTRL written as 0x%02x: vane uses Bypass, FIFO or Continuous mode only", v)
		}
	case agCtrl9:
		if v&^uint8(agFIFOEn|agStopOnFTH) != 0 {
			return m.regs.violation("CTRL_REG9 written as 0x%02x: vane sets only FIFO_EN and STOP_ON_FTH", v)
		}
	}
	return nil
}

func (m *LSM9DS1AccelGyroModel) afterWrite(reg int) {
	switch reg {
	case agFIFOCtrl, agCtrl9:
		if !m.fifoOn() {
			m.fifo, m.ovrn = m.fifo[:0], false // Bypass resets the FIFO (p. 23)
		}
	}
}

func (m *LSM9DS1AccelGyroModel) afterRead(reg int) {
	switch reg {
	case agOutGLast:
		m.regs.Set(agStatus, m.regs.Get(agStatus)&^agStatusGDA)
	case agOutXLLast:
		m.regs.Set(agStatus, m.regs.Get(agStatus)&^agStatusXLDA)
		if m.fifoOn() && len(m.fifo) > 0 {
			m.fifo = append(m.fifo[:0], m.fifo[1:]...)
			m.ovrn = false
			if len(m.fifo) > 0 {
				m.latch(m.fifo[0])
			}
		}
	}
}

// LSM9DS1MagnetometerModel is the magnetometer device of an LSM9DS1 at 0x1C.
//
// It increments by the sub-address MSB, like the HTS221 and LPS25H, and is
// modelled with the same rules: a multi-byte read without the bit is refused,
// though the board showed this device increments either way. vane's rules:
// CTRL_REG5_M keeps BDU set; REBOOT, SOFT_RST, SIM and I2C_DISABLE are never
// written; only CTRL_REG1_M to CTRL_REG5_M are written.
//
// Tick is one ODR period in continuous mode; in single mode it takes one sample
// and returns MD to power-down, as the datasheet describes (p. 19).
type LSM9DS1MagnetometerModel struct {
	regs *Regs
	opts modelOptions
	cur  Axes
}

var _ Device = (*LSM9DS1MagnetometerModel)(nil)

// NewLSM9DS1Magnetometer returns the magnetometer at 0x1C, at its power-on state.
func NewLSM9DS1Magnetometer(opts ...ModelOption) *LSM9DS1MagnetometerModel {
	m := &LSM9DS1MagnetometerModel{opts: newModelOptions(opts)}
	m.regs = NewRegs(m.opts.rep, magAddr, magCtrl1, magCtrl2, magCtrl3, magCtrl4, magCtrl5)
	m.Reset()
	return m
}

// Addr implements Device.
func (m *LSM9DS1MagnetometerModel) Addr() uint16 { return magAddr }

// Peek returns a register's value, bypassing the rules.
func (m *LSM9DS1MagnetometerModel) Peek(reg uint8) uint8 { return m.regs.Get(reg) }

// Poke stores a register's value, bypassing the rules.
func (m *LSM9DS1MagnetometerModel) Poke(reg, val uint8) { m.regs.Set(reg, val) }

// Reset returns the device to its power-on state (datasheet Table 21, p. 40):
// CTRL_REG1_M 0x10, CTRL_REG3_M 0x03 (power-down). The board showed a chip
// whose registers did not match this after a halt, which is why the driver
// configures everything it relies on.
func (m *LSM9DS1MagnetometerModel) Reset() {
	for reg := range regCount {
		m.regs.Set(uint8(reg), 0) // #nosec G115 -- reg < 128
	}
	id := uint8(0x3D)
	if m.opts.whoAmI != nil {
		id = *m.opts.whoAmI
	}
	m.regs.Set(magWhoAmI, id)
	m.regs.Set(magCtrl1, 0x10)
	m.regs.Set(magCtrl3, 0x03)
}

// Set gives the value the next Tick samples, in counts.
func (m *LSM9DS1MagnetometerModel) Set(v Axes) { m.cur = v }

// Tick is one ODR period.
func (m *LSM9DS1MagnetometerModel) Tick() {
	md := m.regs.Get(magCtrl3) & magMDMask
	if md != magMDCont && md != magMDSingle {
		return
	}
	for i := range 3 {
		setLE16(m.regs, magOut+uint8(2*i), m.cur[i]) // #nosec G115 -- i < 3
	}
	st := m.regs.Get(magStatus)
	if st&magZYXDA != 0 {
		st |= magZYXOR
	}
	m.regs.Set(magStatus, st|magZYXDA|0x07)
	if md == magMDSingle {
		m.regs.Set(magCtrl3, m.regs.Get(magCtrl3)|magMDMask)
	}
}

// Tx implements Device.
func (m *LSM9DS1MagnetometerModel) Tx(w, rd []byte) error {
	if len(w) > 1 && len(rd) == 0 {
		reg := int(w[0] & regMask)
		for i, v := range w[1:] {
			if err := m.checkWrite(reg+i, v); err != nil {
				return err
			}
		}
	}
	if err := m.regs.Tx(w, rd); err != nil {
		return err
	}
	// The data-ready and overrun bits clear once the outputs are read: seen on
	// the board for a full six-byte read, with OUT_Z_H_M standing for it.
	if len(rd) > 0 && covers(int(w[0]&regMask), len(rd), magOutLast) {
		m.regs.Set(magStatus, 0)
	}
	return nil
}

func (m *LSM9DS1MagnetometerModel) checkWrite(reg int, v uint8) error {
	switch reg {
	case magCtrl2:
		if v&(1<<3|1<<2) != 0 {
			return m.regs.violation("CTRL_REG2_M written as 0x%02x: vane never sets REBOOT or SOFT_RST", v)
		}
	case magCtrl3:
		if v&(1<<7|1<<2) != 0 {
			return m.regs.violation("CTRL_REG3_M written as 0x%02x: vane never sets I2C_DISABLE or SIM", v)
		}
	case magCtrl5:
		if v&magBDU == 0 {
			return m.regs.violation("CTRL_REG5_M written as 0x%02x with BDU = 0: block data update must stay on (RF-06)", v)
		}
	}
	return nil
}
