// Package kernel reads what the Linux kernel says about the I2C devices vane is
// about to use: which driver, if any, is bound to an address, and who the Sense
// HAT says it is.
//
// Both answers come from files, so the root they are read under is a parameter:
// production passes "/" and the tests pass a temporary directory laid out like
// sysfs. A device the kernel owns must not be written to by vane (RF-07). The
// kernel does not stop it: `I2C_RDWR` checks nothing (docs/architecture.md), so
// this check is the only protection, and it covers only a driver bound before
// it ran. A driver bound afterwards is not detected.
package kernel

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

// maxFile caps how much of a device-tree property is read. The properties are
// short strings; the cap keeps an unexpected file from being read whole.
const maxFile = 256

// Owner reports the kernel driver bound to the device at addr on I2C bus
// number bus, as sysfs shows it: the `driver` symlink of
// /sys/bus/i2c/devices/<bus>-<addr as four hex digits>, named after the driver.
//
// A device with no node, or a node with no `driver` link, is not owned: the
// kernel knows of it (or not) but has bound nothing to it, which is how the
// HTS221 sits on a standard install. An error other than a missing path, such
// as a permission failure, is returned: the answer is not known, and "not owned"
// would be a guess that lets vane write to a chip it should leave alone.
func Owner(root string, bus int, addr uint16) (driver string, owned bool, err error) {
	link := filepath.Join(root, "sys", "bus", "i2c", "devices", fmt.Sprintf("%d-%04x", bus, addr), "driver")
	target, err := os.Readlink(link)
	switch {
	case err == nil:
		return filepath.Base(target), true, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case errors.Is(err, syscall.EINVAL):
		return "", false, fmt.Errorf("kernel: %s exists and is not a symbolic link", link)
	default:
		return "", false, fmt.Errorf("kernel: reading %s: %w", link, err)
	}
}

// Board returns the Sense HAT's identity from the device tree, where the
// firmware exposes the HAT's EEPROM under /proc/device-tree/hat. ok is false
// when it is not there, which is not an error: a HAT without an EEPROM, a
// kernel that did not read it, and a machine with no HAT all look the same, and
// the EEPROM only informs, it never identifies a chip (ADR-0003).
//
// The strings come from a device on the bus, so they are made safe to print:
// control characters and invalid UTF-8 are replaced.
func Board(root string) (product, vendor, productVer string, ok bool) {
	dir := filepath.Join(root, "proc", "device-tree", "hat")
	product, ok = readProperty(filepath.Join(dir, "product"))
	if !ok {
		return "", "", "", false
	}
	vendor, _ = readProperty(filepath.Join(dir, "vendor"))
	productVer, _ = readProperty(filepath.Join(dir, "product_ver"))
	return product, vendor, productVer, true
}

func readProperty(path string) (string, bool) {
	// The path is built from a fixed layout under the injected root. os.Root would
	// confine it, but it refuses absolute symbolic links and /proc/device-tree is
	// one (it points at /sys/firmware/devicetree/base), so it would break the
	// read on the Pi, where this cannot be tested from here.
	f, err := os.Open(path) // #nosec G304 -- fixed layout under the injected root; see above
	if err != nil {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxFile))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", false
	}
	return sanitize(string(bytes.TrimRight(b, "\x00\r\n\t "))), true
}

// sanitize makes a string from an untrusted device safe to print.
func sanitize(s string) string {
	s = strings.ToValidUTF8(s, "?")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r < 0xA0) || r == utf8.RuneError {
			return '?'
		}
		return r
	}, s)
}

var busPath = regexp.MustCompile(`^/dev/i2c-(\d+)$`)

// BusNumber extracts N from a device path of the form /dev/i2c-N. ok is false
// for any other path, such as the name of a fake bus, in which case there is no
// sysfs node to look at.
func BusNumber(path string) (n int, ok bool) {
	m := busPath.FindStringSubmatch(path)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Bus is an I2C adapter the kernel knows of.
type Bus struct {
	Number int
	// Path is the device node, such as /dev/i2c-1.
	Path string
	// Name is the adapter's own name, such as "bcm2835 (i2c@7e804000)", or empty
	// if sysfs does not give one.
	Name string
}

// Buses lists the I2C adapters under /sys/class/i2c-dev, the way `i2cdetect -l`
// does, in order of number. A system with none, or with no such directory, has an
// empty list and no error: that is an answer, not a failure.
func Buses(root string) ([]Bus, error) {
	dir := filepath.Join(root, "sys", "class", "i2c-dev")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("kernel: listing %s: %w", dir, err)
	}
	var buses []Bus
	for _, e := range entries {
		n, ok := strings.CutPrefix(e.Name(), "i2c-")
		if !ok {
			continue
		}
		num, err := strconv.Atoi(n)
		if err != nil || num < 0 {
			continue
		}
		name, _ := readProperty(filepath.Join(dir, e.Name(), "name"))
		buses = append(buses, Bus{Number: num, Path: fmt.Sprintf("/dev/i2c-%d", num), Name: name})
	}
	slices.SortFunc(buses, func(a, b Bus) int { return a.Number - b.Number })
	return buses, nil
}
