package lps25h

// DefaultAddress is the LPS25H's I2C address on the Sense HAT.
const DefaultAddress = 0x5C

// Register map, from the sources reproduced in docs/hardware.md. The vanetest
// model repeats these numbers on purpose and does not import them: sharing them
// would let a wrong constant here agree with a wrong constant there.
const (
	regWhoAmI = 0x0F
	regCtrl1  = 0x20
	regCtrl2  = 0x21
	regStatus = 0x27
	regOut    = 0x28 // PRESS_OUT_XL, _L, _H, TEMP_OUT_L, _H

	whoAmIValue = 0xBD // the LPS25H and the LPS25HB answer the same

	ctrl1PD       = 1 << 7
	ctrl1ODRShift = 4 // ODR is bits 6:4; 0 selects one-shot
	ctrl1BDU      = 1 << 2
	ctrl2OneShot  = 1 << 0
	statusTDA     = 1 << 0
	statusPDA     = 1 << 1
	statusTOR     = 1 << 4
	statusPOR     = 1 << 5
)
