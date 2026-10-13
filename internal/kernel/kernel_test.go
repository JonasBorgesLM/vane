package kernel_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/vane/internal/kernel"
)

// sysfs builds a root laid out like /sys/bus/i2c: a device node per entry, each
// optionally bound to a driver by a relative symlink, as the kernel makes them.
type sysfs struct {
	t    *testing.T
	root string
}

func newSysfs(t *testing.T) *sysfs { return &sysfs{t: t, root: t.TempDir()} }

func (s *sysfs) mkdir(parts ...string) string {
	s.t.Helper()
	p := filepath.Join(append([]string{s.root}, parts...)...)
	if err := os.MkdirAll(p, 0o750); err != nil {
		s.t.Fatal(err)
	}
	return p
}

// device makes the node bus-addr; driver "" leaves it unbound.
func (s *sysfs) device(node, driver string) {
	s.t.Helper()
	dir := s.mkdir("sys/bus/i2c/devices", node)
	if driver == "" {
		return
	}
	s.mkdir("sys/bus/i2c/drivers", driver)
	if err := os.Symlink("../../../../bus/i2c/drivers/"+driver, filepath.Join(dir, "driver")); err != nil {
		s.t.Fatal(err)
	}
}

func TestOwnerNamesTheBoundDriver(t *testing.T) {
	s := newSysfs(t)
	s.device("1-005c", "st-press-i2c") // the LPS25H, bound by the rpi-sense overlay
	s.device("1-001c", "st-lsm9ds1-magn")

	for _, tc := range []struct {
		bus  int
		addr uint16
		want string
	}{{1, 0x5C, "st-press-i2c"}, {1, 0x1C, "st-lsm9ds1-magn"}} {
		driver, owned, err := kernel.Owner(s.root, tc.bus, tc.addr)
		if err != nil || !owned || driver != tc.want {
			t.Errorf("Owner(%d, %#x) = %q, %v, %v; want %q, true, nil", tc.bus, tc.addr, driver, owned, err, tc.want)
		}
	}
}

func TestADeviceWithNoDriverIsNotOwned(t *testing.T) {
	s := newSysfs(t)
	s.device("1-005f", "") // the HTS221's node exists and is not bound
	driver, owned, err := kernel.Owner(s.root, 1, 0x5F)
	if err != nil || owned || driver != "" {
		t.Errorf("Owner = %q, %v, %v; want unowned and no error", driver, owned, err)
	}
}

func TestMissingPathsAreNotOwnedAndNotAnError(t *testing.T) {
	s := newSysfs(t)
	s.device("1-005c", "st-press-i2c")
	for name, root := range map[string]string{
		"an address with no node":    s.root,
		"a bus with no devices":      s.root,
		"no sysfs at all":            t.TempDir(),
		"a root that does not exist": filepath.Join(s.root, "nope"),
	} {
		bus, addr := 1, uint16(0x77)
		if name == "a bus with no devices" {
			bus, addr = 7, 0x5C
		}
		driver, owned, err := kernel.Owner(root, bus, addr)
		if err != nil || owned || driver != "" {
			t.Errorf("%s: Owner = %q, %v, %v; want unowned and no error", name, driver, owned, err)
		}
	}
}

// The node name is bus, a dash, and the address as four lower-case hex digits.
// Negative control, seen failing: two digits, or upper case, finds nothing and
// reports every device as unowned.
func TestTheNodeNameIsBusDashFourHexDigits(t *testing.T) {
	s := newSysfs(t)
	s.device("0-005c", "on-bus-0")
	s.device("10-005c", "on-bus-10")
	s.device("1-0000", "at-zero")
	for _, tc := range []struct {
		bus  int
		addr uint16
		want string
	}{{0, 0x5C, "on-bus-0"}, {10, 0x5C, "on-bus-10"}, {1, 0, "at-zero"}} {
		if driver, owned, _ := kernel.Owner(s.root, tc.bus, tc.addr); !owned || driver != tc.want {
			t.Errorf("Owner(%d, %#x) = %q, %v; want %q", tc.bus, tc.addr, driver, owned, tc.want)
		}
	}
	if _, owned, _ := kernel.Owner(s.root, 1, 0x5C); owned {
		t.Error("bus 1 has no node at 0x5c but was reported owned")
	}
}

// sysfs on Linux is case-sensitive and the kernel names its nodes in lower case.
// A filesystem that ignores case, as the default one on macOS does, cannot tell
// the two spellings apart, so the test skips there and the Linux CI runs it.
func TestTheNodeNameIsLowerCase(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "a")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.ToUpper(probe)); err == nil {
		t.Skip("this filesystem ignores case")
	}
	s := newSysfs(t)
	s.device("1-005C", "st-press-i2c") // spelled in upper case: not what the kernel makes
	if _, owned, _ := kernel.Owner(s.root, 1, 0x5C); owned {
		t.Error("a node spelled 1-005C was found for 0x5c: the lookup is not lower case")
	}
	s.device("1-005c", "st-press-i2c")
	if _, owned, _ := kernel.Owner(s.root, 1, 0x5C); !owned {
		t.Error("the lower-case node was not found")
	}
}

func TestADanglingDriverLinkStillNamesTheDriver(t *testing.T) {
	s := newSysfs(t)
	dir := s.mkdir("sys/bus/i2c/devices/1-005c")
	if err := os.Symlink("../../../../bus/i2c/drivers/gone", filepath.Join(dir, "driver")); err != nil {
		t.Fatal(err)
	}
	if driver, owned, err := kernel.Owner(s.root, 1, 0x5C); err != nil || !owned || driver != "gone" {
		t.Errorf("Owner = %q, %v, %v; the link names a driver even if its target is gone", driver, owned, err)
	}
}

func TestADriverEntryThatIsNotALinkIsAnErrorNotAGuess(t *testing.T) {
	s := newSysfs(t)
	dir := s.mkdir("sys/bus/i2c/devices/1-005c")
	if err := os.WriteFile(filepath.Join(dir, "driver"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, owned, err := kernel.Owner(s.root, 1, 0x5C); err == nil || owned {
		t.Errorf("Owner = owned %v, err %v; want an error: the answer is not known", owned, err)
	}
}

func TestAnUnreadablePathIsAnErrorNotAGuess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	if runtime.GOOS == "windows" {
		t.Skip("a Windows directory mode does not stop reads, so the condition cannot be made")
	}
	s := newSysfs(t)
	dir := s.mkdir("sys/bus/i2c/devices/1-005c")
	s.device("1-005c", "st-press-i2c")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // #nosec G302 -- a directory must be searchable for TempDir to remove it
	if _, owned, err := kernel.Owner(s.root, 1, 0x5C); err == nil || owned {
		t.Errorf("Owner = owned %v, err %v; want an error, since \"not owned\" would let vane write to a chip it cannot see", owned, err)
	}
}

func writeProp(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "proc", "device-tree", "hat")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBoardReadsTheHATEEPROM(t *testing.T) {
	root := t.TempDir()
	writeProp(t, root, "product", "Raspberry Pi Sense HAT\x00") // device-tree strings end in NUL
	writeProp(t, root, "vendor", "Raspberry Pi Foundation\x00")
	writeProp(t, root, "product_ver", "0x0002\x00")
	product, vendor, ver, ok := kernel.Board(root)
	if !ok || product != "Raspberry Pi Sense HAT" || vendor != "Raspberry Pi Foundation" || ver != "0x0002" {
		t.Errorf("Board = %q, %q, %q, %v", product, vendor, ver, ok)
	}
}

func TestBoardWithoutAnEEPROMIsAbsentNotAnError(t *testing.T) {
	for name, root := range map[string]string{"empty root": t.TempDir(), "root that does not exist": filepath.Join(t.TempDir(), "nope")} {
		product, vendor, ver, ok := kernel.Board(root)
		if ok || product != "" || vendor != "" || ver != "" {
			t.Errorf("%s: Board = %q, %q, %q, %v; want nothing", name, product, vendor, ver, ok)
		}
	}
}

func TestBoardWithOnlyAProductStillCountsAsPresent(t *testing.T) {
	root := t.TempDir()
	writeProp(t, root, "product", "Sense HAT\n")
	product, vendor, ver, ok := kernel.Board(root)
	if !ok || product != "Sense HAT" || vendor != "" || ver != "" {
		t.Errorf("Board = %q, %q, %q, %v", product, vendor, ver, ok)
	}
}

func TestBoardWithoutAProductIsAbsent(t *testing.T) {
	root := t.TempDir()
	writeProp(t, root, "vendor", "Someone")
	if _, _, _, ok := kernel.Board(root); ok {
		t.Error("a HAT directory with a vendor and no product was reported present")
	}
}

// What an EEPROM says is untrusted text that ends up on a terminal. Negative
// control, seen failing: returning the bytes as read.
func TestBoardTextIsMadeSafeToPrint(t *testing.T) {
	root := t.TempDir()
	writeProp(t, root, "product", "Sense\x1b[31m HAT\x07\xff\x00")
	writeProp(t, root, "vendor", "Pi\r\nFoundation\u0085")
	product, vendor, _, ok := kernel.Board(root)
	if !ok {
		t.Fatal("not present")
	}
	for name, s := range map[string]string{"product": product, "vendor": vendor} {
		for _, r := range s {
			if r < 0x20 || r == 0x7F || (r >= 0x80 && r < 0xA0) {
				t.Errorf("%s %q still holds the control character %U", name, s, r)
			}
		}
	}
	if !strings.HasPrefix(product, "Sense") || !strings.Contains(product, "HAT") {
		t.Errorf("product = %q, the printable text should survive", product)
	}
	if strings.Contains(vendor, "\n") || strings.Contains(vendor, "\r") {
		t.Errorf("vendor = %q", vendor)
	}
}

func TestBoardReadsAtMostAFewHundredBytes(t *testing.T) {
	root := t.TempDir()
	writeProp(t, root, "product", strings.Repeat("A", 100000))
	product, _, _, ok := kernel.Board(root)
	if !ok || len(product) != 256 {
		t.Errorf("len(product) = %d, ok %v; want it capped at 256", len(product), ok)
	}
}

func TestBusNumber(t *testing.T) {
	for _, tc := range []struct {
		path string
		n    int
		ok   bool
	}{
		{"/dev/i2c-1", 1, true},
		{"/dev/i2c-0", 0, true},
		{"/dev/i2c-14", 14, true},
		{"/dev/i2c-", 0, false},
		{"/dev/i2c-1x", 0, false},
		{"/dev/i2c-1/", 0, false},
		{"i2c-1", 0, false},
		{"/dev/ttyS0", 0, false},
		{"fake-bus", 0, false},
		{"", 0, false},
		{"/dev/i2c--1", 0, false},
		{"/dev/i2c-99999999999999999999", 0, false}, // does not fit an int
	} {
		n, ok := kernel.BusNumber(tc.path)
		if n != tc.n || ok != tc.ok {
			t.Errorf("BusNumber(%q) = %d, %v; want %d, %v", tc.path, n, ok, tc.n, tc.ok)
		}
	}
}
