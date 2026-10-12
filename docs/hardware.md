# Hardware facts

What vane relies on from the Sense HAT chips, with where each fact was read. A
fact here is **Confirmed** only when a primary source says it: kernel code,
the vendor's reference code, or a datasheet. Facts that still need the Pi or
the datasheet are listed separately, so nothing is assumed by omission.

Observations from the board itself (M1, #14–#16) are added to the end of this
file when they exist; until then nothing in it comes from the Pi.

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

## Still to confirm

| Fact | Needed for | Where it can be settled |
| --- | --- | --- |
| Conversion time per averaging setting | the one-shot ready timeout (default 1 s is a deliberate over-estimate) | datasheet table, then measured on the Pi (#14) |
| Operating range: humidity 0–100 %RH, temperature −40…+120 °C | `FlagOutOfRange` | datasheet front page; written from memory in the driver |
| Whether reading the reserved registers 0x34, 0x38, 0x39 inside the 0x30–0x3F calibration block is harmless | the driver reads that block in one transaction | first transcript on the Pi (#30) |
| Whether `ONE_SHOT` is ignored while `PD` = 0 | what the model does after a chip reset | datasheet; the model assumes it is ignored, which is the cautious reading |
| Whether `ONE_SHOT` reads back 1 while a conversion is in progress and 0 once it ends | recovery after a timeout: the driver asks the chip whether a conversion is still pending instead of guessing whether it reset | datasheet (`CTRL_REG2` description); the ST header names the bit but not its read-back |
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

### Still to confirm for the LPS25H

| Fact | Needed for | Where it can be settled |
| --- | --- | --- |
| The averaging sample counts per `AVGP`/`AVGT` code | an averaging option | datasheet, for each of the two chips |
| Conversion time (default averaging) | the one-shot ready timeout (default 1 s is an over-estimate) | datasheet, then measured on the Pi (#14) |
| Operating range: pressure 260–1260 hPa, temperature −30…+105 °C | `FlagOutOfRange`; only 1260 is confirmed (full scale) | datasheet; the driver has the rest from memory |
| Whether `P_OR`/`T_OR` are raised when `BDU` is on and a sample is held back | whether `FlagOverrun` can ever appear, since vane keeps `BDU` on | datasheet, or a long run on the Pi |
| Whether reading the output registers clears `P_DA`/`T_DA` and the overrun bits | the model's flag handling | datasheet |
| Whether `ONE_SHOT` reads back 1 while a conversion is pending | recovery after a timeout (as for the HTS221) | datasheet |

The LSM9DS1 facts, and everything about kernel ownership on the Pi, are in
REQUIREMENTS Appendix A and still await M1.
