# ADR-0007: Writes are restricted to each chip's datasheet-writable registers, enforced by the test models, not at runtime

## Status
Accepted — 2026-10-12

## Context
Any process allowed to open `/dev/i2c-1` can write to every device on the bus,
including the Sense HAT microcontroller at 0x46, where one register controls the
HAT EEPROM's write protection (G-10). vane's drivers write only a handful of
configuration registers on two chips. The risk vane itself introduces is a
**driver bug**: a wrong address, a wrong register constant, a missing
auto-increment bit turning a block write into something else.

The v0.1 survey proposed a runtime decorator (`internal/busmw`) that refuses any
write outside a declared list, motivated by "group membership grants access to
the whole bus".

## Decision
- Each register model in `vanetest` declares, **from the datasheet**, the
  registers the host may write. The model fails the test on any write outside
  that set, to any address it does not model, or as a multi-byte write without
  the auto-increment bit.
- Because the models import nothing from `chip/*` (enforced by
  `check-boundaries.sh`), the allowed set is an independent oracle: a driver
  with a wrong constant fails against it instead of agreeing with itself.
- No runtime allowlist in v0.1. The drivers are the only code that writes, and
  every write path runs against the models in CI.

## The alternative that was rejected
**A runtime allowlist decorator around `vane.Bus`.** It catches the same class
of bug — vane's own drivers writing where they should not — but only after the
code has shipped, at the cost of a layer on every transaction and a list the
driver itself would supply (the oracle agreeing with the code it checks).

Its stated motive does not hold: a runtime check inside vane **cannot** restrict
other processes in the i2c group, which can open the same device and write
anything. Presenting it as bus protection would describe a control as complete
when it covers only vane's own writes.

## Consequences
- **Residual:** nothing in vane protects the bus from other processes with
  access to `/dev/i2c-1`, including writes to the 0x46 EEPROM write-protect
  register. That is the operating system's permission model, documented in
  `SECURITY.md` as out of scope.
- A write path not exercised in tests is not checked. Driver tests must cover
  every configuration path (BDU, both modes, every averaging setting, `Close`).
- A third-party driver added later would be checked only if it ships a model.

## Reopening criterion
A third-party driver is accepted into vane, or a write outside a model's set is
observed in production (a transcript recorded on the Pi that a model rejects).
