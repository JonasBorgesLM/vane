// Package lsm9ds1 drives the ST LSM9DS1 inertial module over a vane.Bus: its
// accelerometer and gyroscope at one address and its magnetometer at another.
//
// One Sensor holds both devices (ADR-0008). New identifies each before writing
// anything to it, configures it with block data update on, and runs both
// continuously: the chip has no one-shot. A device that does not identify is
// left untouched and its reads return the identification error, while the other
// keeps working; New fails only when neither identifies (ADR-0006).
//
// Read returns the latest accelerometer and gyroscope sample, ReadMagnetic the
// latest magnetometer sample. A quantity the chip has no newer sample of is
// flagged vane.FlagStale, a magnetometer sample the chip overwrote unread is
// flagged vane.FlagOverrun, and an axis at the limit of its 16-bit count, where
// the chip saturates, is flagged vane.FlagClamped.
//
// Units are the datasheet's (ADR-0008): vane.StandardGravity, vane.DegreesPerSecond
// and vane.Gauss, converted with the sensitivity of the configured full scale
// (DocID025715 Rev 3, Table 3).
//
// The accelerometer and gyroscope increment their register address by a
// register bit, CTRL_REG8.IF_ADD_INC, not by the sub-address MSB the other ST
// chips use; on the board an MSB-set sub-address read another register
// (docs/hardware.md). The package keeps that bit set and never sends the MSB to
// that device.
package lsm9ds1
