package hts221

// DefaultAddress is the HTS221's I2C address.
const DefaultAddress = 0x5F

// Register map, from the datasheet as reproduced in docs/hardware.md. The
// vanetest model repeats these numbers on purpose and does not import them:
// sharing them would let a wrong constant here agree with a wrong constant
// there.
const (
	regWhoAmI = 0x0F
	regAvConf = 0x10
	regCtrl1  = 0x20
	regCtrl2  = 0x21
	regStatus = 0x27
	regOut    = 0x28 // HUMIDITY_OUT_L, _H, TEMP_OUT_L, _H

	whoAmIValue = 0xBC

	ctrl1PD      = 1 << 7
	ctrl1BDU     = 1 << 2
	ctrl1ODRMask = 0x03
	ctrl2OneShot = 1 << 0
	statusTDA    = 1 << 0
	statusHDA    = 1 << 1
)
