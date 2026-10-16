package conformance

import (
	"bytes"
	"testing"
	"time"
)

// LSM9DS1 numbers, repeated from DocID025715 Rev 3, not taken from a driver or
// a model.
const (
	agAddr  = 0x6A
	magAddr = 0x1C

	agCtrl1G  = 0x10
	agCtrl6XL = 0x20
	agCtrl8   = 0x22
	agCtrl9   = 0x23
	agStatus  = 0x27
	agOutG    = 0x18
	agOutXL   = 0x28
	agFIFOCtl = 0x2E
	agFIFOSrc = 0x2F

	magCtrl3  = 0x22
	magCtrl5  = 0x24
	magStatus = 0x27
	magOut    = 0x28

	odr119       = 3 << 5 // ODR_G / ODR_XL = 119 Hz, the rate the board observations used
	bduIfAddInc  = 0x44   // CTRL_REG8: BDU, IF_ADD_INC
	fifoEn       = 0x02   // CTRL_REG9
	fmodeFIFO    = 1 << 5
	fmodeContin  = 6 << 5
	agPeriod     = 1000 * time.Millisecond / 119
	magPeriod    = 100 * time.Millisecond // CTRL_REG1_M reset ODR, 10 Hz
	statusGDA    = 1 << 1
	statusXLDA   = 1 << 0
	magZYXDA     = 1 << 3
	fifoOVRN     = 1 << 6
	fifoFSSMask  = 0x3F
	magPowerDown = 0x03
)

// agDev is raw access to the accelerometer and gyroscope, whose block reads
// send the bare sub-address: its auto-increment is CTRL_REG8.IF_ADD_INC, and on
// the board an MSB-set sub-address read another register.
type agDev struct{ dev }

func (d agDev) block(reg uint8, n int) []byte {
	d.t.Helper()
	r := make([]byte, n)
	if err := d.bus.Tx(bg, d.addr, []byte{reg}, r); err != nil {
		d.t.Fatalf("reading %d bytes from register 0x%02x of 0x%02x: %v", n, reg, d.addr, err)
	}
	return r
}

// LSM9DS1 checks both devices of the chip. The target's bus carries both.
func LSM9DS1(t *testing.T, tg Target) {
	tg.prepare()
	ag := agDev{dev{t: t, bus: tg.Bus, addr: agAddr}}
	mag := dev{t: t, bus: tg.Bus, addr: magAddr}

	off := func() {
		ag.write(agCtrl8, bduIfAddInc) // BDU on from here: vane never turns it off (RF-06)
		ag.write(agFIFOCtl, 0)
		ag.write(agCtrl9, 0)
		ag.write(agCtrl1G, 0)
		ag.write(agCtrl6XL, 0)
		mag.write(magCtrl5, 0x40) // BDU
		mag.write(magCtrl3, magPowerDown)
	}
	t.Cleanup(off)

	t.Run("identity", func(t *testing.T) {
		ag.t, mag.t = t, t
		if got := ag.read(0x0F); got != 0x68 {
			t.Errorf("accelerometer/gyroscope WHO_AM_I = %#x, want 0x68 (p. 45)", got)
		}
		if got := mag.read(0x0F); got != 0x3D {
			t.Errorf("magnetometer WHO_AM_I_M = %#x, want 0x3d (p. 40)", got)
		}
	})

	t.Run("a burst reads the gyroscope, then the accelerometer", func(t *testing.T) {
		ag.t = t
		off()
		ag.write(agCtrl1G, odr119)
		ag.write(agCtrl6XL, odr119)
		tg.advance(5 * agPeriod)
		// Freeze the outputs so single reads and the burst see one sample.
		ag.write(agCtrl1G, 0)
		ag.write(agCtrl6XL, 0)
		g, xl := ag.block(agOutG, 6), ag.block(agOutXL, 6)
		burst := ag.block(agOutG, 12)
		if !bytes.Equal(burst, append(g, xl...)) {
			t.Errorf("12 bytes from OUT_X_G = %x, want OUT_G %x then OUT_XL %x: the burst does not jump from 0x1D to 0x28", burst, g, xl)
		}
		observe(t, "24 bytes from OUT_X_G with both sensors off: %x", ag.block(agOutG, 24))
	})

	t.Run("data-ready bits", func(t *testing.T) {
		ag.t = t
		off()
		ag.write(agCtrl1G, odr119)
		ag.write(agCtrl6XL, odr119)
		tg.advance(3 * agPeriod)
		st := ag.read(agStatus)
		observe(t, "STATUS_REG with both sensors on: %#x (GDA=%v XLDA=%v)", st, st&statusGDA != 0, st&statusXLDA != 0)
		ag.block(agOutG, 6)
		// ASSUMPTION in the model: GDA clears when OUT_Z_G_H is read, XLDA when
		// OUT_Z_XL_H is. A new sample can arrive between the reads at 119 Hz.
		observe(t, "STATUS_REG after reading OUT_G: %#x", ag.read(agStatus))
		ag.block(agOutXL, 6)
		observe(t, "STATUS_REG after reading OUT_XL: %#x", ag.read(agStatus))
	})

	t.Run("a FIFO sample leaves when its accelerometer bytes are read", func(t *testing.T) {
		ag.t = t
		off()
		ag.write(agCtrl1G, odr119)
		ag.write(agCtrl6XL, odr119)
		ag.write(agCtrl9, fifoEn)
		ag.write(agFIFOCtl, fmodeContin)
		tg.advance(12 * agPeriod)
		ag.write(agCtrl1G, 0) // stop filling, so the count only moves by reading
		ag.write(agCtrl6XL, 0)
		n := int(ag.read(agFIFOSrc) & fifoFSSMask)
		observe(t, "FIFO_SRC after the sensors were powered down: %d unread", n)
		if n < 4 {
			t.Skipf("only %d samples kept after power-down: cannot count reads", n)
		}
		ag.block(agOutG, 6)
		if got := int(ag.read(agFIFOSrc) & fifoFSSMask); got != n {
			t.Errorf("FSS after the gyroscope bytes = %d, want %d: the sample left too early", got, n)
		}
		ag.block(agOutXL, 6)
		if got := int(ag.read(agFIFOSrc) & fifoFSSMask); got != n-1 {
			t.Errorf("FSS after the accelerometer bytes = %d, want %d", got, n-1)
		}
		ag.block(agOutG, 12)
		if got := int(ag.read(agFIFOSrc) & fifoFSSMask); got != n-2 {
			t.Errorf("FSS after a 12-byte burst = %d, want %d: one gyroscope-plus-accelerometer burst takes out one sample", got, n-2)
		}
		// Longer bursts wrap, and with the gyroscope off the datasheet wraps
		// OUT_Z_XL to OUT_X_XL (p. 21), so what 24 bytes take out depends on that.
		ag.block(agOutG, 24)
		observe(t, "FSS after a 24-byte burst with both sensors off: %d (from %d)", ag.read(agFIFOSrc)&fifoFSSMask, n-2)
	})

	t.Run("FIFO mode stops at 32 and reports OVRN", func(t *testing.T) {
		ag.t = t
		off()
		ag.write(agCtrl1G, odr119)
		ag.write(agCtrl6XL, odr119)
		ag.write(agCtrl9, fifoEn)
		ag.write(agFIFOCtl, fmodeFIFO)
		tg.advance(40 * agPeriod)
		src := ag.read(agFIFOSrc)
		if src&fifoFSSMask != 32 || src&fifoOVRN == 0 {
			t.Errorf("FIFO_SRC = %#x, want 32 unread and OVRN (board: 0x60)", src)
		}
		ag.block(agOutG, 12)
		if src := ag.read(agFIFOSrc); src&fifoOVRN != 0 {
			observe(t, "OVRN still set after a sample was taken out in FIFO mode: %#x", src)
		}
		ag.write(agFIFOCtl, 0)
		if got := ag.read(agFIFOSrc); got != 0 {
			t.Errorf("FIFO_SRC after Bypass = %#x, want 0: Bypass empties the FIFO (p. 23)", got)
		}
	})

	t.Run("the magnetometer in continuous and single mode", func(t *testing.T) {
		mag.t = t
		off()
		mag.write(magCtrl3, 0x00)
		tg.advance(3 * magPeriod)
		if st := mag.read(magStatus); st&magZYXDA == 0 {
			t.Errorf("STATUS_REG_M = %#x in continuous mode, want ZYXDA", st)
		}
		mag.block(magOut, 6)
		// ASSUMPTION in the model: the data-ready bits clear when OUT_Z_H_M is read.
		observe(t, "STATUS_REG_M after reading the six output bytes: %#x", mag.read(magStatus))
		mag.write(magCtrl3, 0x01)
		tg.advance(3 * magPeriod)
		observe(t, "CTRL_REG3_M after a single conversion: %#x (the model returns MD to 11)", mag.read(magCtrl3))
	})
}
