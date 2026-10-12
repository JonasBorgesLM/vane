# Security

## Reporting

Report privately through GitHub: **Security → Report a vulnerability** on this
repository. Do not open a public issue. Expect an answer within a week; this is
a one-maintainer project.

## What counts

vane runs with read/write access to an I2C bus. In scope:

- vane writing to an address or register its drivers do not declare
  (ADR-0007), including the HAT EEPROM write-protect register at 0x46.
- vane accessing a chip a kernel driver owns, or forcing an address (RF-07).
- A crafted bus response (malicious or faulty device) causing a panic,
  unbounded allocation or a hang that ignores the context.

Out of scope: anything another process in the `i2c` group can do — group
membership grants access to the whole bus, and vane cannot restrict it.

## Verifying a release

Release tags are signed with a key listed in
[`.github/allowed_signers`](.github/allowed_signers), and every file attached to
a GitHub release has build provenance:

```bash
git -c gpg.ssh.allowedSignersFile=.github/allowed_signers tag -v vX.Y.Z
gh attestation verify vane-vX.Y.Z.tar --repo JonasBorgesLM/vane
```
