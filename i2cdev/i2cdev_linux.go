//go:build linux

package i2cdev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/JonasBorgesLM/vane"
)

// From include/uapi/linux/i2c-dev.h and include/uapi/linux/i2c.h, checked in the
// raspberrypi/linux rpi-6.12.y tree. x/sys/unix defines neither.
const (
	ioctlRDWR = 0x0707 // I2C_RDWR: combined read/write transfer, one STOP only
	msgRead   = 0x0001 // I2C_M_RD: this message reads
)

// msg is struct i2c_msg. Go lays its fields out as the C compiler does, with
// the pointer aligned after the three 16-bit fields, on 32- and 64-bit alike;
// a test checks the offsets.
type msg struct {
	addr  uint16
	flags uint16
	len   uint16
	buf   *byte
}

// rdwrData is struct i2c_rdwr_ioctl_data.
type rdwrData struct {
	msgs  *msg
	nmsgs uint32
}

// Bus is an I2C adapter opened through /dev/i2c-N. It is safe for concurrent
// use; the kernel serialises each transfer on the adapter, and the mutex keeps
// a Close from racing a transfer on the same descriptor.
type Bus struct {
	mu   sync.Mutex
	path string
	fd   int

	// The ioctl arguments live in the Bus, on the heap, so they cannot move while
	// the kernel reads them through the pointers inside them, and so a
	// transaction allocates nothing. Guarded by mu.
	msgs [2]msg
	data rdwrData
}

var _ vane.Bus = (*Bus)(nil)

// Open opens the adapter device at path, such as "/dev/i2c-1".
//
// A permission failure is vane.ErrPermission and says which group usually owns
// the device; a missing device says that the I2C interface may be disabled.
func Open(path string) (*Bus, error) {
	var fd int
	var err error
	for {
		fd, err = unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, openError(path, err)
	}
	return &Bus{path: path, fd: fd}, nil
}

func openError(path string, err error) error {
	switch {
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return fmt.Errorf("%w: cannot open %s: %w; the user needs read and write access to it, which on Raspberry Pi OS means the i2c group",
			vane.ErrPermission, path, err)
	case errors.Is(err, unix.ENOENT):
		return fmt.Errorf("i2cdev: %s does not exist: is the I2C interface enabled (raspi-config, or dtparam=i2c_arm=on)? %w", path, err)
	default:
		return fmt.Errorf("i2cdev: opening %s: %w", path, err)
	}
}

// Name returns the device path the Bus was opened with.
func (b *Bus) Name() string { return b.path }

// Close closes the device. It is idempotent, and a Tx after it returns
// vane.ErrClosed.
func (b *Bus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fd < 0 {
		return nil
	}
	fd := b.fd
	b.fd = -1
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("i2cdev: closing %s: %w", b.path, err)
	}
	return nil
}

// Tx implements vane.Bus. It writes w to the device at addr and then reads
// len(r) bytes into r as one transfer with a repeated start; an empty w means no
// write and an empty r no read. The context is checked before the transfer
// starts. A transfer in progress cannot be interrupted, and Tx does not retry.
func (b *Bus) Tx(ctx context.Context, addr uint16, w, r []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(w) == 0 && len(r) == 0 {
		// An address-only write is the "quick command" i2cdetect uses to probe, and
		// some chips take it as a write. Nothing in vane needs it.
		return errors.New("i2cdev: an empty transaction would be a quick command, which vane does not send")
	}
	if len(w) > math.MaxUint16 || len(r) > math.MaxUint16 {
		return fmt.Errorf("i2cdev: a message of %d or %d bytes does not fit the 16-bit length of an i2c_msg", len(w), len(r))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fd < 0 {
		return vane.ErrClosed
	}
	n := b.build(addr, w, r)

	var res uintptr
	var errno unix.Errno
	for {
		// The argument is a pointer to a structure held by b, so it is on the heap
		// and stays where the kernel will read it for the whole call.
		res, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(b.fd), ioctlRDWR, uintptr(unsafe.Pointer(&b.data))) // #nosec G103 -- the pointer is to a heap structure of b, converted in the call expression
		if errno != unix.EINTR {
			break
		}
	}
	return b.outcome(res, errno, addr, n)
}

// outcome decides what the ioctl's result means. An errno is mapped. Otherwise
// the return value is how many messages the kernel carried, and fewer than were
// asked for is a transfer that stopped part-way: the read buffer cannot be
// trusted and the call is not a success.
func (b *Bus) outcome(res uintptr, errno unix.Errno, addr uint16, want int) error {
	if errno != 0 {
		return b.transferError(errno, addr)
	}
	if int(res) != want {
		return fmt.Errorf("i2cdev: %s at 0x%02x: %d of %d messages transferred: %w", b.path, addr, res, want, io.ErrUnexpectedEOF)
	}
	return nil
}

// build fills the ioctl arguments for a write, a read, or a write then a read,
// and returns the number of messages. The caller holds b.mu.
func (b *Bus) build(addr uint16, w, r []byte) int {
	n := 0
	if len(w) > 0 {
		b.msgs[n] = msg{addr: addr, len: uint16(len(w)), buf: &w[0]} // #nosec G115 -- len(w) was checked against MaxUint16
		n++
	}
	if len(r) > 0 {
		b.msgs[n] = msg{addr: addr, flags: msgRead, len: uint16(len(r)), buf: &r[0]} // #nosec G115 -- len(r) was checked against MaxUint16
		n++
	}
	b.data = rdwrData{msgs: &b.msgs[0], nmsgs: uint32(n)} // #nosec G115 -- n is 1 or 2
	return n
}

// transferError maps the errno of a failed transfer. ENXIO is the adapter
// reporting that nothing acknowledged the address; EREMOTEIO that the device
// refused a data byte. Both mean the device is not there for vane's purposes,
// and the errno is kept so the difference can still be read.
func (b *Bus) transferError(errno unix.Errno, addr uint16) error {
	switch errno {
	case unix.ENXIO, unix.EREMOTEIO:
		return fmt.Errorf("%w: %w", vane.ErrNotPresent, errno)
	case unix.EOPNOTSUPP:
		return fmt.Errorf("i2cdev: %s does not support combined transfers: %w", b.path, errno)
	default:
		return fmt.Errorf("i2cdev: %s at 0x%02x: I2C_RDWR: %w", b.path, addr, errno)
	}
}
