# vane

[![CI](https://github.com/JonasBorgesLM/vane/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/JonasBorgesLM/vane/actions/workflows/ci.yml)

Go driver for the environmental sensors of the Raspberry Pi **Sense HAT** —
**HTS221** (relative humidity, temperature) and **LPS25H/LPS25HB** (pressure,
temperature) — on a **Raspberry Pi 5**, `linux/arm64`, **without cgo**.

Part of a small ecosystem of Go projects (moat, bastion, cistern, crier, cairn,
sapper, warden, usher). The name comes from the weather vane on top of a tower:
the instrument that read the environment.

> **Status: v0.1.0.** Both drivers, the board API, the test fakes and
> `vane-probe` work and are tested on a Pi 5.
> Progress is on the [project board](https://github.com/users/JonasBorgesLM/projects/8).

## What it does

```go
dev, err := sensehat.Open(ctx)               // probes the board, checks kernel ownership
if err != nil { /* no chip could be opened */ }
defer dev.Close()

env, err := dev.Environment(ctx)             // partial failure is per sensor
if env.Humidity.Err == nil {
    fmt.Println(env.Humidity.Value, "%RH")   // vane.RelativeHumidity, not a bare float
}
for _, t := range env.Temperatures {         // one per chip, HTS221 first, uncompensated
    fmt.Println(t.Source.Chip, t.Value, t.Err)
}
```

- **Typed units** — `vane.Celsius`, `vane.Hectopascal`, `vane.RelativeHumidity`;
  every measurement also carries the raw ADC count, its source (chip, bus,
  address), the acquisition time and quality flags.
- **Partial failure** — one chip failing does not hide the others' readings.
- **Identification before any write** — each chip's WHO_AM_I is checked first;
  a wrong chip receives nothing.
- **Respects the kernel** — when a kernel driver is bound to a chip (the
  Sense HAT overlay declares the LPS25H for one; whether it binds depends on
  the kernel build), vane detects that and refuses with an explanation instead
  of fighting the kernel's driver for the chip.
- **Testable without hardware** — `vanetest` offers a register-level fake bus
  for driver tests and a board fake for consumers.
- **One dependency** — `golang.org/x/sys`, in the Linux backend only. Everything
  else compiles and tests on any OS.

## What it does not do

- Compensate temperature for the Pi's CPU heat, or compute dew point or any
  other derived quantity. It reports each chip's temperature so the consumer can.
  On a Pi 5 in a case the HAT reads about 37 °C at room temperature.
- Retry, cache, serialise or transport readings — that is the consumer's job.
- Log. Errors carry chip, address, register and operation instead.
- Drive the IMU, the colour sensor, the LED matrix or the joystick — not in v0.1.
- Coordinate with another process using the same chips: serialisation is per
  process.

## Setting up the Pi

Tested on a Raspberry Pi 5 with a Sense HAT v2 and Raspberry Pi OS 64-bit
(Debian 13, kernel 6.12). Each step says how to check it, so a step that is
already done can be skipped. Commands run on the Pi unless they say otherwise.

### 1. Turn on I2C

```sh
ls /dev/i2c-1 || sudo raspi-config nonint do_i2c 0
```

`do_i2c 0` sets `dtparam=i2c_arm=on` in `/boot/firmware/config.txt`; reboot if
it ran. `/dev/i2c-1` must exist afterwards.

### 2. Give your user the bus

vane needs read and write access to `/dev/i2c-1`, not root:

```sh
ls -l /dev/i2c-1                 # crw-rw---- 1 root i2c ... on Raspberry Pi OS
id -nG | grep -qw i2c || sudo usermod -aG i2c "$USER"
```

After `usermod`, log out and in again (or reboot) for the group to apply. A
missing permission shows as `vane.ErrPermission`, naming the device.

Other arm64 distributions are best-effort (RNF-13) and **not tested**. If
`ls -l` shows a group other than `i2c`, join that group instead. If the device
is `root:root`, as some Ubuntu images ship it, give it a group with a udev
rule, then reboot:

```sh
sudo groupadd -f i2c && sudo usermod -aG i2c "$USER"
echo 'KERNEL=="i2c-[0-9]*", GROUP="i2c", MODE="0660"' | sudo tee /etc/udev/rules.d/60-i2c.rules
```

### 3. Keep the kernel off the sensors (recommended)

The HAT's EEPROM loads an overlay that declares the pressure sensor and the IMU
for kernel drivers. Whether a driver binds depends on the kernel build; when one
does, vane refuses that chip rather than fight it ([ADR-0001](docs/adr/0001-hardware-access-and-kernel-drivers.md)).
The `vane-sense` overlay disables those nodes so no driver can bind, and keeps
the LED matrix and the joystick working:

```sh
curl -fsSLO https://raw.githubusercontent.com/JonasBorgesLM/vane/v0.1.0/overlays/vane-sense.dts
dtc -@ -I dts -O dtb -o vane-sense.dtbo vane-sense.dts
sudo cp vane-sense.dtbo /boot/firmware/overlays/
grep -qx 'dtoverlay=vane-sense' /boot/firmware/config.txt || echo 'dtoverlay=vane-sense' | sudo tee -a /boot/firmware/config.txt
sudo reboot
```

`dtc` is in the `device-tree-compiler` package if it is missing. After the
reboot, check that the firmware loaded it:

```sh
sudo vclog -m | grep "Loaded overlay 'vane-sense'"
ls /sys/bus/i2c/devices          # no 1-001c, 1-005c, 1-006a
```

If the first line prints nothing, the overlay was not applied. **Keep every
line of `config.txt` short, comments included**: on a Pi 5 the firmware
silently skipped a `dtoverlay` line that followed a 98-character comment.

### 4. Check with `vane-probe`

The Pi does not need Go. Build on your computer (Go 1.26 or newer) and copy it
over:

```sh
# on your computer
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go install github.com/JonasBorgesLM/vane/cmd/vane-probe@v0.1.0
scp "$(go env GOPATH)/bin/linux_arm64/vane-probe" pi@raspberrypi.local:
```

A cross-compiled `go install` puts the binary under `bin/linux_arm64/`, not
`bin/`.

With Go 1.26 or newer on the Pi itself, `go install
github.com/JonasBorgesLM/vane/cmd/vane-probe@v0.1.0` does the same. Then, on
the Pi:

```sh
./vane-probe -require all
```

A ready board ends with `2 of 2 chips usable by vane.` and exit status 0.

## Testing without a Pi

A consumer tests against `vanetest.FakeBoard`, which has the method set of
`*sensehat.Device` and answers `Environment` from a script, one step per call.
Declare the interface you use, as beacon does, and pass either:

```go
type board interface {
    Environment(context.Context) (sensehat.Environment, error)
    Close() error
}

at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
var b board = vanetest.NewFakeBoard(
    vanetest.Healthy(at, 50, 1013.25, 25, 22.5),                                     // %RH, hPa, °C HTS221, °C LPS25H
    vanetest.Healthy(at, 50, 1013.25, 25, 22.5).Failing(vane.ChipLPS25H, vane.ErrNotPresent),
)
env, err := b.Environment(ctx) // err == nil, every reading valid
env, err = b.Environment(ctx)  // err == nil, env.Pressure.Err is ErrNotPresent
```

A call past the end of the script returns `vanetest.ErrScriptEnded`, so a
consumer that polls more often than the test planned finds out. Drivers are
tested one level down, against register models on `vanetest.FakeBus`.

## Looking at a board

`vane-probe` is a read-only check of what vane can do with a Sense HAT: it lists
the I2C adapters, says for each chip whether the kernel already drives it, and
for the free ones reads the identity register. It never writes to the bus.

`-require hts221`, `lps25h` or `all` turns a chip that vane cannot use into
exit status 1, for a deployment check. `-bus` examines another adapter.

The exit status is 0 when nothing prevents vane from working, 1 when the bus
cannot be opened or a required chip is not usable, and 2 for a bad command line.

## Latency

How long one one-shot `Read` takes, trigger to converted sample, measured on
a Raspberry Pi 5 with a Sense HAT v2 (median of 20 reads, `TestHardwareLatency`;
detail in [`docs/hardware.md`](docs/hardware.md)):

| Chip | Averaging (humidity/temperature samples) | Median |
| --- | --- | --- |
| HTS221 | 4/2, 8/4, 16/8, **32/16 (default)** | 7.0 ms |
| HTS221 | 64/32 | 12.5 ms |
| HTS221 | 128/64 | 18.1 ms |
| HTS221 | 256/128 | 29.2 ms |
| HTS221 | 512/256 | 51.3 ms |
| LPS25H | chip default (no averaging option yet) | 40.3 ms |

The driver polls the status register every 5 ms (`WithPollInterval`), so these
figures are the conversion time rounded up to that step plus the bus traffic,
not the chip's conversion time alone.

## Requirements

Raspberry Pi 5 with a Sense HAT (v1 or v2), Raspberry Pi OS 64-bit, read/write
access to `/dev/i2c-1` (the `i2c` group — no root). Go 1.26 or newer to build.

Setting the Pi up is the [section above](#setting-up-the-pi).

## Documents

| | |
| --- | --- |
| [`REQUIREMENTS.md`](REQUIREMENTS.md) | What vane must do, with the ids cited everywhere else |
| [`docs/adr/`](docs/adr/README.md) | Decisions, and the ones still open |
| [`docs/hardware.md`](docs/hardware.md) | What the chips and the board were observed to do, with the evidence |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Git flow, branch names, commit format |
| [`RELEASING.md`](RELEASING.md) | How a version is cut and verified |
| [`SECURITY.md`](SECURITY.md) | Reporting a vulnerability; verifying a release |
| [`CHANGELOG.md`](CHANGELOG.md) | What changed, per version |

## License

[MIT](LICENSE)
