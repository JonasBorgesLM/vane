//go:build linux

package i2cdev

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/JonasBorgesLM/vane"
)

var bg = context.Background()

// The kernel reads these structures through pointers, so their layout is the
// kernel's ABI: a field in the wrong place is a corrupted transfer, not a
// compile error. The sizes are checked against the pointer width because the
// same code builds for a 32-bit Pi OS.
func TestStructLayoutMatchesTheKernelsStructures(t *testing.T) {
	ptr := unsafe.Sizeof(uintptr(0))
	var m msg
	if got := unsafe.Offsetof(m.addr); got != 0 {
		t.Errorf("offsetof addr = %d, want 0", got)
	}
	if got := unsafe.Offsetof(m.flags); got != 2 {
		t.Errorf("offsetof flags = %d, want 2", got)
	}
	if got := unsafe.Offsetof(m.len); got != 4 {
		t.Errorf("offsetof len = %d, want 4", got)
	}
	if got := unsafe.Offsetof(m.buf); got != 8 {
		t.Errorf("offsetof buf = %d, want 8: the pointer follows three u16 and two bytes of padding", got)
	}
	if got, want := unsafe.Sizeof(m), 8+ptr; got != want {
		t.Errorf("sizeof(msg) = %d, want %d", got, want)
	}
	var d rdwrData
	if got := unsafe.Offsetof(d.nmsgs); got != ptr {
		t.Errorf("offsetof nmsgs = %d, want %d", got, ptr)
	}
	if got, want := unsafe.Sizeof(d), 2*ptr; got != want {
		t.Errorf("sizeof(rdwrData) = %d, want %d", got, want)
	}
}

// From include/uapi/linux/i2c-dev.h and i2c.h in rpi-6.12.y. A typo here sends a
// different request to the kernel.
func TestRequestNumbersAreTheKernels(t *testing.T) {
	if ioctlRDWR != 0x0707 {
		t.Errorf("I2C_RDWR = %#x, want 0x0707", ioctlRDWR)
	}
	if msgRead != 0x0001 {
		t.Errorf("I2C_M_RD = %#x, want 0x0001", msgRead)
	}
}

// Negative control, seen failing: building the read message without I2C_M_RD, or
// in front of the write, turns a register read into a second write.
func TestBuildShapesTheMessages(t *testing.T) {
	w, r := []byte{0x0F}, make([]byte, 3)
	tests := []struct {
		name    string
		w, r    []byte
		wantN   int
		flags   []uint16
		lengths []uint16
	}{
		{"write then read", w, r, 2, []uint16{0, msgRead}, []uint16{1, 3}},
		{"write only", []byte{0x20, 0x85}, nil, 1, []uint16{0}, []uint16{2}},
		{"read only", nil, r, 1, []uint16{msgRead}, []uint16{3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b Bus
			n := b.build(0x5F, tc.w, tc.r)
			if n != tc.wantN || int(b.data.nmsgs) != tc.wantN || b.data.msgs != &b.msgs[0] {
				t.Fatalf("n = %d, nmsgs = %d; want %d messages starting at msgs[0]", n, b.data.nmsgs, tc.wantN)
			}
			for i := range tc.wantN {
				m := b.msgs[i]
				if m.addr != 0x5F || m.flags != tc.flags[i] || m.len != tc.lengths[i] || m.buf == nil {
					t.Errorf("msg %d = %+v, want addr 0x5f flags %#x len %d", i, m, tc.flags[i], tc.lengths[i])
				}
			}
			if len(tc.w) > 0 && b.msgs[0].buf != &tc.w[0] {
				t.Error("the write message does not point at the caller's write buffer")
			}
			if len(tc.r) > 0 && b.msgs[tc.wantN-1].buf != &tc.r[0] {
				t.Error("the read message does not point at the caller's read buffer, so the kernel would fill a copy")
			}
		})
	}
}

func TestBuildAllocatesNothing(t *testing.T) {
	var b Bus
	w, r := []byte{0x0F}, make([]byte, 4)
	if n := testing.AllocsPerRun(200, func() { b.build(0x5F, w, r) }); n != 0 {
		t.Fatalf("build allocates %v times per call, want 0", n)
	}
}

func TestTransferErrorMapping(t *testing.T) {
	b := &Bus{path: "/dev/i2c-1"}
	tests := []struct {
		errno      unix.Errno
		notPresent bool
	}{
		{unix.ENXIO, true},     // nothing acknowledged the address
		{unix.EREMOTEIO, true}, // the device refused a data byte
		{unix.EIO, false},
		{unix.ETIMEDOUT, false},
		{unix.EOPNOTSUPP, false},
		{unix.ENOTTY, false},
		{unix.EBUSY, false},
	}
	for _, tc := range tests {
		err := b.transferError(tc.errno, 0x5C)
		if got := errors.Is(err, vane.ErrNotPresent); got != tc.notPresent {
			t.Errorf("%v: errors.Is(ErrNotPresent) = %v, want %v (%v)", tc.errno, got, tc.notPresent, err)
		}
		if !errors.Is(err, tc.errno) {
			t.Errorf("%v: the errno was lost from %v", tc.errno, err)
		}
	}
	if err := b.transferError(unix.EOPNOTSUPP, 0x5C); !errors.Is(err, unix.EOPNOTSUPP) || err.Error() == unix.EOPNOTSUPP.Error() {
		t.Errorf("EOPNOTSUPP says nothing about combined transfers: %v", err)
	}
}

func TestOpenAMissingDeviceExplainsWhy(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "i2c-1"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want one that matches fs.ErrNotExist", err)
	}
	if got := err.Error(); !contains(got, "I2C interface enabled") {
		t.Errorf("the error does not mention that I2C may be disabled: %v", err)
	}
}

func TestOpenWithoutPermissionIsErrPermissionWithAHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens anything")
	}
	path := filepath.Join(t.TempDir(), "i2c-1")
	if err := os.WriteFile(path, nil, 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if !errors.Is(err, vane.ErrPermission) || !errors.Is(err, unix.EACCES) {
		t.Fatalf("error = %v, want ErrPermission wrapping EACCES", err)
	}
	if got := err.Error(); !contains(got, "i2c group") {
		t.Errorf("the error does not name the group: %v", err)
	}
}

// /dev/null opens read-write and is not an I2C adapter, so the ioctl reaches the
// kernel and fails with ENOTTY. That exercises the real syscall path, the struct
// passing included, without hardware, and shows that a failure which is not a
// missing chip is not reported as one.
func TestARealIoctlOnAFileThatIsNotAnAdapterFailsWithENOTTY(t *testing.T) {
	b, err := Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer b.Close()
	err = b.Tx(bg, 0x5F, []byte{0x0F}, make([]byte, 1))
	if !errors.Is(err, unix.ENOTTY) {
		t.Fatalf("error = %v, want one wrapping ENOTTY from the kernel", err)
	}
	if errors.Is(err, vane.ErrNotPresent) {
		t.Errorf("a device that is not an adapter was reported as an absent chip: %v", err)
	}
}

func TestACloseIsIdempotentAndEndsTheBus(t *testing.T) {
	b, err := Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	if b.Name() != "/dev/null" {
		t.Errorf("Name() = %q", b.Name())
	}
	for range 3 {
		if err := b.Close(); err != nil {
			t.Errorf("Close = %v", err)
		}
	}
	if err := b.Tx(bg, 0x5F, []byte{0x0F}, make([]byte, 1)); !errors.Is(err, vane.ErrClosed) {
		t.Errorf("Tx after Close = %v, want ErrClosed", err)
	}
}

func TestRefusedTransactionsNeverReachTheKernel(t *testing.T) {
	b, err := Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer b.Close()
	cctx, cancel := context.WithCancel(bg)
	cancel()
	huge := make([]byte, 1<<16)
	for name, call := range map[string]func() error{
		"a cancelled context":  func() error { return b.Tx(cctx, 0x5F, []byte{1}, nil) },
		"an empty transaction": func() error { return b.Tx(bg, 0x5F, nil, nil) },
		"a write over 65535":   func() error { return b.Tx(bg, 0x5F, huge, nil) },
		"a read over 65535":    func() error { return b.Tx(bg, 0x5F, []byte{1}, huge) },
	} {
		err := call()
		if err == nil || errors.Is(err, unix.ENOTTY) {
			t.Errorf("%s: error = %v; it must be refused before the ioctl, which would answer ENOTTY", name, err)
		}
	}
	if err := b.Tx(cctx, 0x5F, []byte{1}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context = %v, want context.Canceled", err)
	}
}

// Negative control, seen failing: dropping the comparison turns a transfer that
// stopped after the write into a success with an unfilled read buffer.
func TestOutcomeDecidesFromErrnoAndMessageCount(t *testing.T) {
	b := &Bus{path: "/dev/i2c-1"}
	tests := []struct {
		name    string
		res     uintptr
		errno   unix.Errno
		want    int
		wantErr error // nil means success
	}{
		{"both messages carried", 2, 0, 2, nil},
		{"the one message carried", 1, 0, 1, nil},
		{"only the write was carried", 1, 0, 2, io.ErrUnexpectedEOF},
		{"nothing was carried", 0, 0, 1, io.ErrUnexpectedEOF},
		{"more than asked for is not trusted either", 3, 0, 2, io.ErrUnexpectedEOF},
		{"an errno wins over the count", 2, unix.ENXIO, 2, vane.ErrNotPresent},
		{"an I/O error", 0, unix.EIO, 2, unix.EIO},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := b.outcome(tc.res, tc.errno, 0x5C, tc.want)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Errorf("error = %v, want success", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("error = %v, want one wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
