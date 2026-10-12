// Package vane holds the types shared by the Sense HAT drivers: units,
// measurements, their source, errors and the Bus seam.
//
// It is a leaf: it imports no other package of this module, so the chip
// drivers can depend on it and the board facade can depend on the drivers
// without a cycle.
package vane
