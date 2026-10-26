package tcs3400

// DefaultAddress is the TCS34001/3 address, the one on the Sense HAT v2 tested.
// The TCS34005/7 answer at AltAddress.
const (
	DefaultAddress = 0x39
	AltAddress     = 0x29
)

// Register map, from the datasheet v2-00 (DS000411). The vanetest model repeats
// these numbers on purpose and does not import them.
const (
	regEnable  = 0x80 // AEN 1, PON 0
	regATime   = 0x81
	regControl = 0x8F // AGAIN 1:0
	regID      = 0x92
	regStatus  = 0x93 // ASAT 7, AVALID 0
	regData    = 0x94 // CDATAL, CDATAH, RDATAL, ..., BDATAH

	idFamily     = 0b100100 // ID bits 7:2
	enableOn     = 0x03     // PON | AEN
	enableOff    = 0x00
	statusAVALID = 1 << 0
	statusASAT   = 1 << 7
	cycle        = 2780 // µs per integration cycle (p. 15)
)
