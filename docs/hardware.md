# Hardware facts

What vane relies on from the Sense HAT chips, with where each fact was read. A
fact here is **Confirmed** only when a primary source says it: kernel code,
the vendor's reference code, or a datasheet. Facts that still need the Pi or
the datasheet are listed separately, so nothing is assumed by omission.

Observations from the board itself (M1, #14–#16) are at the end of this file;
everything above them comes from sources, not from the Pi.

## HTS221 — confirmed from primary sources

Sources, read on 2026-10-12:

- **Kernel** — `raspberrypi/linux` branch `rpi-6.12.y`,
  `drivers/iio/humidity/hts221_core.c` and `hts221_i2c.c`.
- **ST reference** — `STMicroelectronics/hts221-pid`, `hts221_reg.h`.

| Fact | Value | Source |
| --- | --- | --- |
| I2C address | 0x5F | `rpi-sense` overlay (Appendix A of REQUIREMENTS) |
| `WHO_AM_I` | register 0x0F = 0xBC | kernel `hts221_core.c:22–23`; ST `hts221_reg.h:167` |
| `AV_CONF` | register 0x10; `AVGH` bits 2:0, `AVGT` bits 5:3 | ST `hts221_reg.h:168–180`; kernel `hts221_core.c:61–90` (masks 0x07 at line 64, 0x38 at line 78) |
| Humidity averaging (`AVGH` 0…7) | 4, 8, 16, 32, 64, 128, 256, 512 samples | kernel `hts221_core.c:66–73` |
| Temperature averaging (`AVGT` 0…7) | 2, 4, 8, 16, 32, 64, 128, 256 samples | kernel `hts221_core.c:80–87` |
| `CTRL_REG1` | register 0x20; `ODR` bits 1:0, `BDU` bit 2, `PD` bit 7 | ST `hts221_reg.h:182–196`; kernel `hts221_core.c:28–30` |
| `ODR` | 0 = one-shot, 1 = 1 Hz, 2 = 7 Hz, 3 = 12.5 Hz | ST `hts221_reg.h:336–340`; kernel `hts221_core.c:55–59` |
| `CTRL_REG2` | register 0x21; `ONE_SHOT` bit 0, `HEATER` bit 1, `BOOT` bit 7 | ST `hts221_reg.h:198–212` |
| `CTRL_REG3` | register 0x22; `DRDY` bit 2, `PP_OD` bit 6, `DRDY_H_L` bit 7 | ST `hts221_reg.h:214–230` |
| `STATUS_REG` | register 0x27; `T_DA` bit 0, `H_DA` bit 1 | ST `hts221_reg.h:232–244` |
| Output | humidity at 0x28–0x29, temperature at 0x2A–0x2B; 16-bit, signed, little-endian | kernel `hts221_core.c:92–124` (addresses 0x28 and 0x2a; `sign = 's'`, `IIO_LE`) |
| Calibration, humidity | `H0_rH_x2` 0x30, `H1_rH_x2` 0x31 (value ÷ 2 = %RH); `H0_T0_OUT` 0x36–0x37, `H1_T0_OUT` 0x3A–0x3B (signed 16-bit LE) | kernel `hts221_core.c:33–36, 302–339`; ST `hts221_reg.h:250–271` |
| Calibration, temperature | `T0_degC_x8` 0x32, `T1_degC_x8` 0x33 (value ÷ 8 = °C), ten bits each: bits 1:0 of 0x35 extend `T0`, bits 3:2 extend `T1`; `T0_OUT` 0x3C–0x3D, `T1_OUT` 0x3E–0x3F (signed 16-bit LE) | kernel `hts221_core.c:37–41, 259–300` |
| Sub-address auto-increment | the **MSB** (0x80) of the register address, for both reads and writes | kernel `hts221_i2c.c:19, 24–25` (`read_flag_mask`, `write_flag_mask = BIT(7)`) |
| Interpolation | linear between the two calibration points, humidity and temperature each | kernel `hts221_core.c:259–339` computes slope `(y1 − y0) / (x1 − x0)` |
| Data-ready flags | `STATUS_REG` has only `T_DA` and `H_DA`: there is **no overrun bit**, so `FlagOverrun` is never set for the HTS221 | ST `hts221_reg.h:232–244` |
| Division by zero | **not guarded by the kernel**: it divides by `cal_x1 − cal_x0` unchecked | kernel `hts221_core.c:294–296, 333–335` |

The last row is why `Calibration.Validate` exists (G-12): the reference driver
trusts the chip, and a chip that reads back identical calibration points is
reported in the field.

## HTS221 — from the datasheet and the board

Datasheet **DocID026333 Rev 4** (August 2016), read on 2026-10-16 from the copy
at `static.rapidonline.com/pdf/73-0218_v1.pdf` (st.com refuses automated
downloads; Rev 5 of March 2023 is the current one and was not read). "Board"
means observed on the Pi 5 (*Observations from the board*, below).

| Fact | Value | Source |
| --- | --- | --- |
| Operating range | humidity 0–100 %RH, temperature −40…+120 °C — what the driver uses for `FlagOutOfRange` and the clamp | datasheet p. 1, Table 3 p. 8 |
| Out-of-range humidity | "must be clipped by SW" — what the driver's clamp does | p. 25 |
| Averaging (`AV_CONF`) | `AVGT` bits 5:3 = 2…256 samples, `AVGH` bits 2:0 = 4…512, default code 011 (16/32) — matches the kernel and the driver | Table 16, p. 21 |
| `ONE_SHOT` | starts one acquisition; at the end of conversion the data are in the output registers, `H_DA`/`T_DA` are set and **`ONE_SHOT` comes back to 0 by hardware**, so it reads 1 while pending | p. 23; board: reads 1 right after the trigger, 0 once both flags are set |
| `ONE_SHOT` written while `PD` = 0 | not stated | board: **latched**, nothing converts, the conversion runs on power-up |
| `H_DA` / `T_DA` | set on a new sample; **cleared when the high byte is read** (`HUMIDITY_OUT_H` 0x29, `TEMP_OUT_H` 0x2B) | p. 24; board: reading only `HUMIDITY_OUT_L` does not clear `H_DA` |
| `STATUS_REG` and BDU | updated on every one-shot and ODR cycle "regardless of the BDU value" | p. 24 |
| BDU | after one half of an output is read, that output is not updated until the other half is | p. 22 |
| Reserved 0x34, 0x38, 0x39 in the calibration block | not described | board: the 16-byte read is clean and stable |

`vanetest`'s models clear a data-ready bit (and, on the LPS25H, its overrun bit)
when the high byte of its output is read, as the datasheets say and the board
showed: `HUMIDITY_OUT_H` alone took `STATUS_REG` from 0x03 to 0x01, `PRESS_OUT_H`
alone from 0x03 to 0x01, and from 0x33 to 0x11 in continuous mode. They used to
wait for every byte, which was stricter than the chip; the drivers always read
the whole block, so no driver behaviour depended on it. The conformance suite
now asserts both halves (low byte alone leaves it set, high byte clears it).

### Still to confirm for the HTS221

| Fact | Needed for | Where it can be settled |
| --- | --- | --- |
| Conversion time per averaging setting | the one-shot ready timeout (default 1 s) | not in the datasheet (Table 16 gives noise and current only); measured on the board as an upper bound, see *Conformance suite on the board* |
| Whether a brown-out leaves `PD` = 0 (so the symptom is `ErrNotReady`) | reset recovery (ADR-0005) | observed only by cutting power to the HAT; otherwise it stays an inference |

## LPS25H / LPS25HB — confirmed from primary sources

Two chips and two sources, so a fact is only as strong as the source that
carries it. Read on 2026-10-12:

- **Kernel** (the LPS25H) — `raspberrypi/linux` `rpi-6.12.y`,
  `drivers/iio/pressure/st_pressure_core.c` and
  `drivers/iio/common/st_sensors/st_sensors_i2c.c`.
- **ST reference** (the LPS25HB) — `STMicroelectronics/lps25hb-pid`,
  `lps25hb_reg.h` and `lps25hb_reg.c`.

| Fact | Value | Source |
| --- | --- | --- |
| I2C address | 0x5C | `rpi-sense` overlay (REQUIREMENTS Appendix A) |
| `WHO_AM_I` | register 0x0F = **0xBD, for both chips** | kernel `st_pressure_core.c:356` (LPS25H); ST `lps25hb_reg.h:189, 199` (LPS25HB) |
| `CTRL_REG1` | register 0x20; `SIM` bit 0, `RESET_AZ` bit 1, `BDU` bit 2, `DIFF_EN` bit 3, `ODR` bits 6:4, `PD` bit 7 | ST `lps25hb_reg.h:214–230`; kernel `st_pressure_core.c:363–375, 392–395` (masks 0x70, 0x80, 0x04) |
| `ODR` | 0 = one-shot, 1 = 1 Hz, 2 = 7 Hz, 3 = 12.5 Hz, 4 = 25 Hz. ST's own enum folds `PD` in: one-shot is 8, the rates are 9 to 12 | kernel `st_pressure_core.c:366–371`; ST `lps25hb_reg.h:470–477` |
| `CTRL_REG2` | register 0x21; `ONE_SHOT` bit 0, `AUTOZERO` bit 1, `SWRESET` bit 2, `FIFO_EN` bit 6, `BOOT` bit 7 | ST `lps25hb_reg.h:232–254` |
| `STATUS_REG` | register 0x27; `T_DA` bit 0, `P_DA` bit 1, `T_OR` bit 4, `P_OR` bit 5. **The overrun bits exist here**, unlike the HTS221 | ST `lps25hb_reg.h:320–338`; kernel `st_pressure_core.c:405–408` (data-ready mask 0x03) |
| Output | pressure 0x28–0x2A, **24-bit signed**, temperature 0x2B–0x2C, **16-bit signed**, little-endian | ST `lps25hb_reg.h:340–344`; kernel `st_pressure_core.c:124–156` (`sign = 's'`, `realbits` 24 and 16) |
| Pressure | hPa = count / 4096 | ST `lps25hb_reg.c:101–104` |
| Temperature | °C = count / 480 + 42.5 | ST `lps25hb_reg.c:106–109`; kernel `st_pressure_core.c:96–97` (480 LSB/°C, offset 42500 m°C) |
| Sub-address auto-increment | the **MSB** (0x80), for multi-byte reads | kernel `st_pressure_core.c:414` (`multi_read_bit`), `st_sensors_i2c.c:18, 25–28, 45` |
| Pressure upper limit | 1260 hPa full scale | kernel `st_pressure_core.c:386` (`ST_PRESS_FS_AVL_1260MB`) |
| Boot time | 2 ms after power-up | kernel `st_pressure_core.c:415` (`bootime`) |
| `AVGP` / `AVGT` | register `RES_CONF` 0x10; `AVGP` bits 1:0, `AVGT` bits 3:2 — **the codes only, see the conflict below** | ST `lps25hb_reg.h:200–212` |

**The LPS25H and the LPS25HB, from these sources.** They share the identity
value and every register the driver uses, as the table shows from two
independent sources. No register read distinguishes them in either source, so
`Sensor.Variant()` returns "unknown" (ADR-0003). That says nothing about
differences in registers vane does not use.

**A conflict, not a fact: the averaging sample counts.** ST's header for the
LPS25HB encodes `AVGP` as 8, 16, 32, 64 samples (`lps25hb_reg.h:440–443`) and
`AVGT` as 8, 16, 32, 64 (`:452–455`). The LPS25H datasheet table, as remembered
and not re-read, gives 8, 32, 128, 512 for pressure. One of the two is wrong
for one of the two chips, and the kernel does not touch `RES_CONF`. Until the
datasheet settles it, vane leaves `RES_CONF` alone and offers no averaging
option for the LPS25H.

**Settled on 2026-10-16 by both datasheets: the header is wrong for pressure.**
LPS25H DocID023722 Rev 6 (Tables 16–17, p. 24) and LPS25HB DocID027112 Rev 4
(Tables 18–19, p. 34) agree: `AVGP` 00…11 = **8, 32, 128, 512** pressure
samples, `AVGT` 00…11 = 8, 16, 32, 64 temperature samples. The chips differ in
the reset value: `RES_CONF` = **0x05** on the LPS25H (32/16), **0x0F** on the
LPS25HB (512/64). The board reads `RES_CONF` = 0x0F, and vane never writes it,
so the Sense HAT v2 tested most likely carries an LPS25HB — a hint, not an
identification (ADR-0003), since anything on the bus can write that register.

The bit positions are not settled by the LPS25H datasheet itself: its register
figure reads `AVGT1 AVGT0 AVGP1 AVGP0` (temperature in bits 3:2) while the
field list under it puts `AVGP` in [3:2]. The LPS25HB datasheet's figure and
ST's header both put `AVGT` in 3:2 and `AVGP` in 1:0. An averaging option must
settle this on the board before writing the register (#17 follow-up).

**Settled on the board (LPS25HB, by its `RES_CONF` reset value): `AVGP` is bits
1:0, `AVGT` bits 3:2**, as the LPS25HB figure and ST's header say. Setting bits
1:0 alone (0x03) lengthens a one-shot conversion from 2.5 ms to 32.6 ms; bits
3:2 alone (0x0C) to 5.5 ms; and pressure averaging is the one that costs. The
one-shot conversion time for every code, timed from the `ONE_SHOT` write to both
data-ready bits by polling `STATUS_REG` as fast as the bus allows (median of 9,
Pi 5, `RES_CONF` restored to 0x0F afterwards):

| Pressure \ temperature samples | 8 | 16 | 32 | 64 |
| --- | --- | --- | --- | --- |
| 8 | 2.5 ms | 3.0 ms | 3.8 ms | 5.5 ms |
| 32 | 3.8 ms | 4.2 ms | 5.1 ms | 7.2 ms |
| 128 | 9.7 ms | 10.2 ms | 11.0 ms | 13.1 ms |
| 512 | 32.6 ms | 33.0 ms | 34.3 ms | 36.0 ms |

`chip/lps25h` offers `WithAveraging` from these facts. A board carrying an
LPS25H rather than an LPS25HB would need the same timing check, since its own
datasheet contradicts itself on the bit positions.

### LPS25H / LPS25HB — from the datasheets and the board

Datasheets **LPS25H DocID023722 Rev 6** (October 2018, copy at
`cdn.components.ru/media/PDF/2284792.pdf`) and **LPS25HB DocID027112 Rev 4**
(August 2016, `cdn.components.ru/media/PDF/2280851.pdf`), read on 2026-10-16.

| Fact | Value | Source |
| --- | --- | --- |
| Operating range | pressure 260–1260 hPa, temperature −30…+105 °C, both chips — what the driver uses | LPS25H p. 1, Table 3 p. 7; LPS25HB p. 1, Table 3 p. 9 |
| Out-of-range pressure | "values exceeding the operating pressure range are clipped" by the chip | LPS25H p. 32 |
| `ONE_SHOT` | starts one measurement when `ODR` = 000; once done it **self-clears**, the data are in the output registers and `STATUS_REG` is updated | LPS25H p. 28; LPS25HB p. 37; board: reads 1 right after the trigger |
| `ONE_SHOT` written while `PD` = 0 | not stated | board: latched, runs on power-up, as the HTS221 |
| `P_DA` / `T_DA` | set on a new sample; cleared when **`PRESS_OUT_H` (0x2A)** / **`TEMP_OUT_H` (0x2C)** is read | LPS25H p. 31; LPS25HB p. 41 |
| `P_OR` / `T_OR` | set when new data arrive while `P_DA`/`T_DA` from the previous ODR cycle was not cleared; cleared with the same high-byte read | LPS25H p. 31; LPS25HB p. 41 |
| `STATUS_REG` and BDU | "updated every ODR cycle, regardless of BDU": with BDU on, an unread sample held back still raises the overrun bit, so **`FlagOverrun` can appear in continuous mode**; in one-shot mode it should not, since the datasheet ties it to ODR cycles (an inference) | LPS25H p. 31; LPS25HB p. 41; board: `P_OR`/`T_OR` 0 after a one-shot and a full read; at 25 Hz with BDU on and nothing read for 1 s, `STATUS_REG` = **0x33**, and reading the 5 output bytes returns it to 0x00 |
| Boot | the `BOOT` process takes 2.2 ms (LPS25HB); the kernel uses 2 ms | LPS25HB p. 37 |

### Still to confirm for the LPS25H

| Fact | Needed for | Where it can be settled |
| --- | --- | --- |
| The `RES_CONF` bit positions on an LPS25H (this board carries an LPS25HB) | `WithAveraging` on such a board | a board with an LPS25H, timing as above |

The LSM9DS1 facts, and everything about kernel ownership on the Pi, are in
REQUIREMENTS Appendix A and still await M1.

## The conformance suite

`internal/conformance` is how the open questions above get settled. One set of
checks runs twice: against the `vanetest` models in CI, and on a Sense HAT with
`-tags hardware`. A check that passes on a model and fails on the chip is a model
error, and the failure message names the assumption.

The checks are an independent client of the chips: raw register access with the
numbers repeated from the datasheet, not imported from the drivers or the models.

Two kinds of statement:

- **Assertions** hold for any correct chip and fail the test: identity, a stable
  calibration block, a one-shot conversion that finishes, `ONE_SHOT` clearing
  itself, data-ready bits clearing once their output was read, a one-shot ignored
  while powered down, the driver leaving the chip powered down on `Close`, and
  `FlagStale` appearing on an immediate second read in continuous mode.
- **Observations** are `OBSERVATION:` lines in the log, for facts the datasheet has
  not confirmed (does `ONE_SHOT` read back 1 while pending; does reading one byte
  clear `H_DA`; the reserved registers inside the calibration block; the overrun
  bits after a full read). They never fail. On the Pi they are the evidence:
  copy each answer into the tables above and correct the model if it disagrees.

Run it on the Pi from the development machine:

```sh
make hw-test HW_HOST=pi@raspberrypi.local HW_EXPECT=custom   # board set up for vane
make hw-test HW_HOST=pi@raspberrypi.local HW_EXPECT=default  # stock overlay
```

`HW_EXPECT=custom` fails if a chip is not free for vane; `default` fails if a chip
is not reported kernel-owned, and in that case no chip is written to. Left empty,
the suite runs what it can and skips the rest. The raw checks never run on a chip a
kernel driver owns. `TestHardwareLatency` logs the one-shot `Read` latency per chip
(min, median, max over 20 reads); output is kept in `hw-results/`, not committed.

It has been run once on a board (see *Observations from the board*); #42 stays
open for the benchmarks and a run against a kernel-owned chip.

## Observations from the board (M1)

Read on 2026-10-14 from one Raspberry Pi 5 Model B Rev 1.1 with a Sense HAT v2,
over SSH, by the Appendix B commands and by `make hw-test`'s test binary. One
board, one OS image: a fact below is "observed here", not "true of every Pi".

### Appendix B, verbatim (#14)

```text
$ uname -r
6.12.62+rpt-rpi-2712
$ head -3 /etc/os-release
PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
VERSION_ID="13"
$ /proc/device-tree/hat/{product,vendor,product_ver}
product: Sense HAT
vendor: Raspberry Pi
product_ver: 0x0002
$ ls -l /dev/i2c-1
crw-rw---- 1 root i2c 89, 1 Jul  9 15:34 /dev/i2c-1
$ id
uid=1000(pi) gid=1000(pi) groups=...,988(i2c),...

$ i2cdetect -l
i2c-1   i2c  Synopsys DesignWare I2C adapter  I2C adapter
i2c-11  i2c  Synopsys DesignWare I2C adapter  I2C adapter
i2c-13  i2c  107d508200.i2c                   I2C adapter
i2c-14  i2c  107d508280.i2c                   I2C adapter

$ i2cdetect -y 1
     0  1  2  3  4  5  6  7  8  9  a  b  c  d  e  f
00:                         -- -- -- -- -- -- -- --
10: -- -- -- -- -- -- -- -- -- -- -- -- 1c -- -- --
20: -- -- -- -- -- -- -- -- -- -- -- -- -- -- -- --
30: -- -- -- -- -- -- -- -- -- 39 -- -- -- -- -- --
40: -- -- -- -- -- -- UU -- -- -- -- -- -- -- -- --
50: -- -- -- -- -- -- -- -- -- -- -- -- 5c -- -- 5f
60: -- -- -- -- -- -- -- -- -- -- 6a -- -- -- -- --
70: -- -- -- -- -- -- -- --

$ driver bound to each address
001c -> no driver
0046 -> simple-mfd-i2c
005c -> no driver
005f -> no driver
006a -> no driver

$ cat /sys/bus/iio/devices/iio:device*/name
(no IIO devices)

$ i2cget -y 1 0x5f 0x0f
0xbc
```

### What this changes

- **G-01 does not hold on this image.** Only `0x46` (the HAT's display and
  joystick MFD) is `UU`. The `rpi-sense` overlay does declare `0x1c`
  (`st,lsm9ds1-magn`), `0x5c` (`st,lps25h-press`) and `0x6a`
  (`st,lsm9ds1-accel`), but each sits in `waiting_for_supplier` with no driver,
  and `/lib/modules/6.12.62+rpt-rpi-2712` ships `hts221*` but **no
  `st_pressure*` or `st_lsm9ds1*`** module. Nothing was loaded, so no IIO device
  exists and the chips are free to vane. This is a property of this kernel
  build, not a promise: a different image or a rebuilt kernel brings the
  conflict back, which is why `Diagnose` and RF-07 stay.
- **The HTS221 (`0x5f`) is not declared in the device tree at all** and
  answers `WHO_AM_I` = 0xBC, as expected.
- **Sense HAT v2 colour sensor at `0x39`**, not `0x29`. `0x29` is empty.
- `/dev/i2c-1` is `root:i2c 0660`, and the default `pi` user is in `i2c`.
  No `sudo` is needed.
- `i2c-tools` is installed but `/usr/sbin` is not on `PATH` for a
  non-interactive SSH session; call `/usr/sbin/i2cdetect`.

### Conformance suite on the board (#42)

`VANE_EXPECT=custom`, all tests passing (`PASS`, exit 0), repeated after every
change below. One-shot `Read` latency, median of 20, per HTS221 averaging
setting (humidity/temperature samples):

| Chip | Averaging | min | median | max |
| --- | --- | --- | --- | --- |
| HTS221 | 4/2 | 6.967 ms | 6.970 ms | 6.982 ms |
| HTS221 | 8/4 | 6.980 ms | 6.982 ms | 6.990 ms |
| HTS221 | 16/8 | 6.972 ms | 6.981 ms | 7.012 ms |
| HTS221 | 32/16 (default) | 6.929 ms | 6.977 ms | 7.054 ms |
| HTS221 | 64/32 | 12.511 ms | 12.520 ms | 12.541 ms |
| HTS221 | 128/64 | 18.061 ms | 18.071 ms | 18.097 ms |
| HTS221 | 256/128 | 29.149 ms | 29.165 ms | 29.412 ms |
| HTS221 | 512/256 | 51.315 ms | 51.335 ms | 51.451 ms |
| LPS25H | chip default | 40.269 ms | 40.332 ms | 40.382 ms |

The steps of about 5.5 ms are the driver's 5 ms poll interval plus the bus
traffic of one poll: the figures bound the conversion time from above, to
within one interval, they do not measure it. Every setting is far under the
1 s ready timeout.

Answers to the open questions, from the `OBSERVATION:` lines and from
`i2cget`/`i2cset` by hand on the free chips:

| Question | Observed |
| --- | --- |
| HTS221: `ONE_SHOT` reads 1 while a conversion is pending | **Yes** |
| HTS221: `ONE_SHOT` clears itself | **Yes**, by the time both `DA` flags are set |
| HTS221: reserved registers 0x34, 0x38, 0x39 inside the 0x30–0x3F block | Harmless; the block reads stable: `3c8ba90f00c4e8ffe30340cb0000a002` |
| HTS221: `H_DA` after reading both humidity bytes | Cleared; `T_DA` stays set (the temperature was not read) |
| HTS221: `H_DA` after reading only `HUMIDITY_OUT_L` | **Not cleared**, as the model assumes |
| LPS25H: `ONE_SHOT` reads 1 while pending | **Yes** |
| LPS25H: `P_OR`/`T_OR` after a one-shot and a full read | Both 0 (`STATUS_REG` = 0x00) |
| `ONE_SHOT` written while `PD` = 0, both chips | **Latched**: reads back 1, nothing converts; on power-up (`PD` = 1) the conversion runs and the bit clears |

The last row disagreed with `vanetest`, whose models dropped the request and
cleared the bit at once. The models now latch it and run it on power-up
(`TestHTS221OneShotWhilePoweredDownRunsOnPowerUp` and the LPS25H twin, seen
failing with the change removed). The drivers never write `ONE_SHOT` to a
powered-down chip, so this changes no driver behaviour; it matters to a model
that must answer like the chip after a sequence the driver did not write.

```text
0x5f PD=0 after ONE_SHOT: ctrl2=0x01 status=0x00
0x5f after power-up: ctrl2=0x00 status=0x03
0x5c PD=0 after ONE_SHOT: ctrl2=0x01 status=0x00
0x5c after power-up: ctrl2=0x00 status=0x03
```

A bug in the suite was found here and fixed: a chip left holding an unread
sample (BDU on) made the HTS221 one-shot check pass its wait before the
conversion started, so `ONE_SHOT` still read 1 and the test failed. The check
now drains the output registers before triggering.

Readings from a Pi in a case with a HAT on top, taken the same minute: about
51 %RH and 37.0 °C (HTS221), 928.8 hPa and 37.6 °C (LPS25H). The 37 °C is the
board warming itself: CPU heat is out of vane's scope.

### `I2C_RDWR` against a kernel-owned address (#15)

**`I2C_RDWR` is not checked; `I2C_SLAVE` is.** In `drivers/i2c/i2c-dev.c` of
`raspberrypi/linux` `rpi-6.12.y` (the branch of the 6.12.62 kernel running
here), `I2C_SLAVE` calls `i2cdev_check_addr` and returns `-EBUSY` when a driver
is bound (lines 409–415, `i2cdev_check` at 188–196); the `I2C_RDWR` case (lines
442–471) hands the messages to `i2cdev_ioctl_rdwr` (243) with each message's
own address and no such check.

Observed: no driver on this image owns a sensor, so one was bound for the
experiment — the kernel's `hts221` driver, instantiated at 0x5F through
`/sys/bus/i2c/devices/i2c-1/new_device` and removed with `delete_device` (and
the modules unloaded) afterwards. A read-only WHO_AM_I:

```text
--- before: no driver
I2C_RDWR  addr 0x5f reg 0xf: value 0xbc err <nil>
I2C_SLAVE addr 0x5f: err <nil>
--- bound: hts221_i2c
50: -- -- -- -- -- -- -- -- -- -- -- -- 5c -- -- UU
I2C_RDWR  addr 0x5f reg 0xf: value 0xbc err <nil>
I2C_SLAVE addr 0x5f: err device or resource busy
--- after delete: no driver
I2C_RDWR  addr 0x5f reg 0xf: value 0xbc err <nil>
```

vane's `i2cdev` issues every transaction as `I2C_RDWR`, so **the sysfs
ownership check is the only thing between vane and a kernel-owned chip**. That
check is what ADR-0001 must rely on, and it does not cover a driver bound
after `Open`.

With the driver bound, vane did refuse, from the sysfs check:

```text
$ vane-probe
Chips on /dev/i2c-1
  HTS221  0x5f   owned by the kernel driver "hts221_i2c"
          vane does not touch a chip the kernel drives. ...
  LPS25H  0x5c   identified
1 of 2 chips usable by vane.        (exit 0; with -require all, exit 1)
```

and the conformance suite (`VANE_EXPECT` empty) skipped every HTS221 check
(`HTS221 is kernel-owned: not touched`) and passed the LPS25H.

### Option E, sysfs unbind (#16, first step)

With the `hts221` driver bound as above, `echo 1-005f >
/sys/bus/i2c/drivers/hts221_i2c/unbind` released the chip at once: the device
node stayed, its `driver` link went, `i2cdetect` showed `5f` again instead of
`UU`, the IIO device disappeared, and `vane-probe` reported both chips
identified. Unbind works and needs root; it does not survive a reboot, and it
does not stop the driver binding again if the device is re-probed.

### `vane-probe` on the board (#41)

```text
vane-probe dev

I2C adapters
  /dev/i2c-1   Synopsys DesignWare I2C adapter   <- examined
  /dev/i2c-11  Synopsys DesignWare I2C adapter
  /dev/i2c-13  107d508200.i2c
  /dev/i2c-14  107d508280.i2c

HAT
  Sense HAT, Raspberry Pi, version 0x0002

Chips on /dev/i2c-1
  HTS221  0x5f   identified
  LPS25H  0x5c   identified

2 of 2 chips usable by vane.
```

Exit status 0.

### The first transcript (#30)

`testdata/transcripts/hts221-pi5-sensehat-v2.txt`, recorded by
`TestRecordTranscript` (`chip/hts221`, `-tags hardware`) and replayed by
`TestRecordedTranscriptsReplay` everywhere. It shows the protocol as the chip
saw it: WHO_AM_I read before any write, one 16-byte calibration read with the
auto-increment bit (`W b0`), `AV_CONF` = 0x1B, `CTRL_REG1` = 0x84 (PD and BDU),
and per read one trigger, two status polls and one 4-byte output read (`W a8`).

### The persistent route: `overlays/vane-sense.dts` (#16)

The overlay disables the three sensor nodes the EEPROM's `rpi-sense-v2`
declares (`lsm9ds1-magn@1c`, `lps25h-press@5c`, `lsm9ds1-accel@6a`) by merging
into them, and leaves `sensehat@46` and the EEPROM alone. The exact
`config.txt` change, against the backup kept on the Pi as
`/boot/firmware/config.txt.before-vane-sense`:

```text
26a27,28
> # vane: sensors to vane, LED and joystick kept
> dtoverlay=vane-sense
```

placed right after `dtoverlay=vc4-kms-v3d`, outside any `[filter]` section, with
`vane-sense.dtbo` in `/boot/firmware/overlays/`. After a real reboot:

```text
$ sudo vclog -m | grep "Loaded overlay"
006750.662: Loaded overlay 'bcm2712d0'
006871.755: Loaded overlay 'rpi-sense-v2'
007031.813: Loaded overlay 'vc4-kms-v3d-pi5'
007175.650: Loaded overlay 'vane-sense'
007502.734: Loaded overlay 'vc4-kms-dsi-ili9881-7inch'
$ ls /sys/bus/i2c/devices
1-0046 11-0045 11-005d i2c-1 i2c-11 i2c-13 i2c-14
```

`1-001c`, `1-005c` and `1-006a` are gone, so no driver can bind to them, under
this kernel or one that ships `st_pressure_i2c`. `0x46` stays `UU`;
`/dev/fb0` is still `RPi-Sense FB` and accepted an 8×8 red frame (written, read
back equal, cleared); the `Raspberry Pi Sense HAT Joystick` input device is
still registered (a key press was not exercised remotely).
`/proc/device-tree/hat` stays, `vane-probe -require all` exits 0, and the
conformance suite passes with `VANE_EXPECT=custom`.

**A firmware trap found on the way.** The first attempt appended the line
after a 98-character comment. The firmware skipped the `dtoverlay` silently,
on three boots, with nothing in `vclog -m` even under `dtdebug=1`, whether the
pair sat at the end of the file under `[all]` or right after `vc4-kms-v3d`.
Shortening the comment to 46 characters was the only change that made it load.
The longest line of the stock file is 75 characters. Whatever the exact limit,
the rule is to keep `config.txt` lines short and to check `vclog` for
`Loaded overlay 'vane-sense'` after the first boot.

### The LPS25H transcript (#35)

`testdata/transcripts/lps25h-pi5-sensehat-v2.txt`, recorded the same way as
the HTS221's (`chip/lps25h`, `TestRecordTranscript`), before the custom overlay
was installed: on this image the stock overlay binds nothing to 0x5C, so the
chip was already free. The bus traffic is the same either way, since the
overlay changes only what the kernel binds. WHO_AM_I (0xBD) is read first,
`CTRL_REG1` = 0x84, and each read is one trigger, eight status polls (about
40 ms at 5 ms) and one 5-byte output read (`W a8`).

### A sample left behind is returned as new (#91)

Found by the beacon spike (#46). The first reading of the day was HTS221
25.91 °C / 74.5 %RH; every later run gave 37.9 °C / 44 %RH, with the LPS25H at
38.4 °C throughout. `STATUS_REG` read `0x03` after each run: a sample was being
left unread.

The mechanism: with BDU on, an unread sample keeps its data-ready bits set. The
v0.1.0 drivers triggered a one-shot without looking, so the wait returned at
once and the **old** sample was read; the conversion just triggered finished
afterwards and was left unread for the next `Read`. Once started, every reading
is one conversion behind — across processes for the HTS221, whose conversion
ends before `Close` powers it down, and within a long-running process for both.
The Pi 5 keeps the HAT powered when halted, so the leftover survives a shutdown.

Decisive test, with the HTS221's heater (never used by vane) to make the old
sample distinguishable: leave a sample unread, heat the chip for 25 s, heater
off, then read three times.

```text
v0.1.0:          38.07 °C   55.73 °C   53.74 °C     (the first is the pre-heat sample)
with the fix:    56.09 °C   54.12 °C   52.87 °C     STATUS_REG 0x00 afterwards
```

The fix is in `New`, which now reads `CTRL_REG2` and the output block before
configuring, writing nothing: a leftover sample is dropped, and a `ONE_SHOT`
latched while powered down (it runs on power-up, see the table above) marks
the sensor dirty, so the first `Read` lets it finish and drops it before its own
trigger. Also checked on the board with a latched one-shot (`CTRL_REG2` 0x01,
`STATUS_REG` 0x03 before `New`): one clean reading, both registers 0x00 after.
`testdata/transcripts/hts221-pi5-sensehat-v2-stale-sample.txt` records the
fixed driver on a chip prepared this way: the dropped block holds raw
temperature 0x0392, the reads that follow about 55.9 °C.

Not covered: another process using the chip at the same time (G-08). A
conversion it triggers in the few milliseconds before `New` reads the output
completes afterwards; the first `Read` then takes it (milliseconds old) and its
own conversion is left unread, which restarts the one-behind chain for that
process. Only concurrent use reaches this; a process killed mid-read leaves a
finished sample, which the next `New` drops.

### Still not settled by this board

- **An image whose kernel ships `st_pressure_i2c`:** the stock conflict G-01
  describes was not reproduced, because this kernel lacks the module. With
  `vane-sense` installed it cannot arise; without it, the LPS25H under a real
  kernel driver (the default-overlay half of #42) is still unobserved.
- Operating ranges and the LPS25H averaging conflict: datasheet questions.
