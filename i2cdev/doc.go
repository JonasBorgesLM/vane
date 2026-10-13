// Package i2cdev is the Linux backend of vane.Bus: it talks to an I2C adapter
// through its /dev/i2c-N device and the i2c-dev driver's combined-transfer
// ioctl.
//
// Every transaction is one I2C_RDWR call with one or two messages, a write and
// then a read joined by a repeated start. The address travels in each message,
// so the file descriptor holds no per-address state and one Bus serves every
// chip on the adapter. The ioctls that bind a descriptor to an address are
// never used. The one that checks whether a kernel driver already owns the
// address is not the one used here, so nothing in the kernel stops a write to a
// chip it owns; internal/kernel is what guards that (RF-07).
//
// golang.org/x/sys/unix has no I2C constants or types, so the request number
// and the two structures are defined here from the kernel's UAPI headers
// (include/uapi/linux/i2c-dev.h and i2c.h).
//
// On any other operating system the package compiles and every operation
// returns vane.ErrUnsupportedPlatform, so code that uses it builds everywhere
// and its tests can run on fakes (RNF-02).
package i2cdev
