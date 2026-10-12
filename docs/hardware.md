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
| `AV_CONF` | register 0x10; `AVGH` bits 2:0, `AVGT` bits 5:3 | ST `hts221_reg.h:168–180`; kernel `hts221_core.c:57–82` (masks 0x07, 0x38) |
| Humidity averaging (`AVGH` 0…7) | 4, 8, 16, 32, 64, 128, 256, 512 samples | kernel `hts221_core.c:60–69` |
| Temperature averaging (`AVGT` 0…7) | 2, 4, 8, 16, 32, 64, 128, 256 samples | kernel `hts221_core.c:73–82` |
| `CTRL_REG1` | register 0x20; `ODR` bits 1:0, `BDU` bit 2, `PD` bit 7 | ST `hts221_reg.h:182–196`; kernel `hts221_core.c:28–30` |
| `ODR` | 0 = one-shot, 1 = 1 Hz, 2 = 7 Hz, 3 = 12.5 Hz | ST `hts221_reg.h:336–340`; kernel `hts221_core.c:51–55` |
| `CTRL_REG2` | register 0x21; `ONE_SHOT` bit 0, `HEATER` bit 1, `BOOT` bit 7 | ST `hts221_reg.h:198–212` |
| `CTRL_REG3` | register 0x22; `DRDY` bit 2, `PP_OD` bit 6, `DRDY_H_L` bit 7 | ST `hts221_reg.h:214–230` |
| `STATUS_REG` | register 0x27; `T_DA` bit 0, `H_DA` bit 1 | ST `hts221_reg.h:232–244` |
| Output | humidity at 0x28–0x29, temperature at 0x2A–0x2B; 16-bit, signed, little-endian | kernel `hts221_core.c:98–122` (`sign = 's'`, `IIO_LE`) |
| Calibration, humidity | `H0_rH_x2` 0x30, `H1_rH_x2` 0x31 (value ÷ 2 = %RH); `H0_T0_OUT` 0x36–0x37, `H1_T0_OUT` 0x3A–0x3B (signed 16-bit LE) | kernel `hts221_core.c:33–36, 308–330`; ST `hts221_reg.h:250–271` |
| Calibration, temperature | `T0_degC_x8` 0x32, `T1_degC_x8` 0x33 (value ÷ 8 = °C), ten bits each: bits 1:0 of 0x35 extend `T0`, bits 3:2 extend `T1`; `T0_OUT` 0x3C–0x3D, `T1_OUT` 0x3E–0x3F (signed 16-bit LE) | kernel `hts221_core.c:37–41, 262–292` |
| Sub-address auto-increment | the **MSB** (0x80) of the register address, for both reads and writes | kernel `hts221_i2c.c:19, 24–25` (`read_flag_mask`, `write_flag_mask = BIT(7)`) |
| Interpolation | linear between the two calibration points, humidity and temperature each | kernel `hts221_core.c:296–330` computes slope `(y1 − y0) / (x1 − x0)` |
| Division by zero | **not guarded by the kernel**: it divides by `cal_x1 − cal_x0` unchecked | kernel `hts221_core.c:292–293, 330–331` |

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
| Whether a brown-out leaves `PD` = 0 (so the symptom is `ErrNotReady`) | reset recovery (ADR-0005) | observed only by cutting power to the HAT; otherwise it stays an inference |

The LPS25H and LSM9DS1 facts, and everything about kernel ownership on the Pi,
are in REQUIREMENTS Appendix A and still await M1.
