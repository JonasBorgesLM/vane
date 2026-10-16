package lsm9ds1

// Default addresses on the Sense HAT.
const (
	DefaultAccelGyroAddress    = 0x6A
	DefaultMagnetometerAddress = 0x1C
)

// Register map, from DocID025715 Rev 3. The vanetest models repeat these
// numbers on purpose and do not import them.
const (
	regWhoAmI = 0x0F // both devices

	agWhoAmIValue = 0x68
	agCtrl1G      = 0x10 // ODR_G 7:5, FS_G 4:3
	agCtrl6XL     = 0x20 // ODR_XL 7:5, FS_XL 4:3
	agCtrl8       = 0x22
	agCtrl9       = 0x23
	agStatus      = 0x27 // GDA 1, XLDA 0
	agOut         = 0x18 // OUT_X_G; a 12-byte burst continues at OUT_X_XL (board)
	agFIFOCtrl    = 0x2E

	// CTRL_REG8: BDU and IF_ADD_INC, nothing else (interrupts active high,
	// push-pull, 4-wire SPI, little-endian).
	agCtrl8Value = 1<<6 | 1<<2
	agStatusXLDA = 1 << 0
	agStatusGDA  = 1 << 1

	magWhoAmIValue = 0x3D
	magCtrl1       = 0x20 // TEMP_COMP 7, OM 6:5, DO 4:2
	magCtrl2       = 0x21 // FS 6:5
	magCtrl3       = 0x22 // MD 1:0
	magCtrl5       = 0x24 // BDU 6
	magStatus      = 0x27 // ZYXOR 7, ZYXDA 3
	magOut         = 0x28 // OUT_X_L_M .. OUT_Z_H_M

	magCtrl1Value  = 4 << 2 // DO = 10 Hz, the reset rate; OM low-power, no temperature compensation
	magCtrl5Value  = 1 << 6 // BDU
	magContinuous  = 0x00
	magPowerDown   = 0x03
	magStatusZYXDA = 1 << 3
	magStatusZYXOR = 1 << 7
)
