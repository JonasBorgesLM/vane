// Package sensehat is the Raspberry Pi Sense HAT as one thing: Open finds the
// environmental sensors on it, and Environment reads them together.
//
// It is the only package that knows the board. The addresses of the chips, the
// check that the kernel does not already drive one of them, and the way a chip
// that is missing or unusable is reported without hiding the others all live
// here; the drivers in chip/hts221 and chip/lps25h know only their own chip.
//
// A failure is per sensor (ADR-0006). Open succeeds with the chips it can use
// unless WithRequired names one it cannot, and Sensors says what happened to
// each. Environment gives every reading its own error, and fails as a whole only
// when the context ends, the device is closed, or nothing could be read.
//
// A Device is safe for concurrent use.
package sensehat
