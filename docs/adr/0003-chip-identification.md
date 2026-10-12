# ADR-0003: Chips are identified by WHO_AM_I before any write; LPS25H and LPS25HB are one family; the HAT EEPROM is information only

## Status
Accepted — 2026-10-12

## Context
Writing configuration to the wrong device can do real damage: the same bus
carries the HAT's microcontroller at 0x46, whose registers include the EEPROM
write protection. The brief asked vane to "fail if the chip is not the expected
one".

Two facts complicate that (REQUIREMENTS G-02):

- The Sense HAT v2 reportedly ships an **LPS25HB** where v1 had the LPS25H. Both
  answer WHO_AM_I `0xBD` and share the register map, so no identity check can
  tell them apart.
- The HAT carries an EEPROM whose product/vendor strings appear under
  `/proc/device-tree/hat/`. It identifies the **board**, not each chip, and
  depends on the overlay and firmware having read it.

## Decision
1. Every driver reads WHO_AM_I (register `0x0F`) **before its first write**. A
   NACK is `ErrNotPresent`; any value other than the expected one is
   `*IdentityError{Want, Got}` and `errors.Is(err, ErrUnexpectedChip)`. No byte
   is written to a device that failed identification (RF-01).
2. `chip/lps25h` accepts `0xBD` as the **LPS25H/LPS25HB family**.
   `Sensor.Variant()` returns `VariantUnknown` unless a difference is found in
   M1 that can be read without side effects; then it may return H or HB.
3. The HAT EEPROM fills `sensehat.BoardInfo` (RF-13). It is never used to
   decide whether a chip is accepted, and its absence is not an error.

## The alternative that was rejected
**Use the EEPROM as a second identity check** (e.g. refuse an LPS25H on a board
that says v2). Rejected: the EEPROM is read by firmware, can be missing or
unreadable, is not guaranteed to say which chips were fitted, and a third-party
board with the same layout would be refused for its label rather than its
chips. WHO_AM_I is read from the chip vane is about to write to, which is the
property that matters.

**Name the driver `lps25hb`, or reject the HB.** Rejected: the register map is
the same, so rejecting it would refuse working hardware, and naming the package
after the newer chip would hide that v1 boards are the common case.

## Consequences
- A substitute chip with a different register map but the same WHO_AM_I value
  would be accepted. No such part is known; transcripts recorded per board
  (RF-11) are where a behavioural difference would surface.
- Identification costs one transaction per chip in `Open` — negligible.
- The probe order is part of the contract: a test asserts the first write in a
  recorded sequence comes after the WHO_AM_I read, because code with the two
  swapped reads identically in review.

## Reopening criterion
M1 or a later board finds a behavioural difference between the LPS25H and
LPS25HB (conversion, timing, a register), or a Sense HAT revision fits a chip
with a different register map behind the same WHO_AM_I.
