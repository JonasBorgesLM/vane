# Changelog

Notable changes to vane. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). In
v0.x a minor version may break the API, and every such break is listed under
**Changed** with a ⚠️ marker.

The release workflow copies the section of the version being tagged into the
GitHub release, next to the API diff it generates, and refuses to publish a tag
with no section here.

## [Unreleased]

## [0.0.1] - 2026-10-12

**Foundations (milestone M0). No library code yet** — this version exists so
`main` holds a released state and the release pipeline runs end to end once
before code depends on it. The module contains only the `vane` package
documentation; there is nothing to import.

### Added

- `REQUIREMENTS.md` v0.2: 31 requirement ids (RF, RNF, RI), the review findings,
  risks, milestones, and why there is no threat model.
- `docs/architecture.md`: package graph with each dependency rule and its
  enforcement, public signatures, the five main flows, the transcript format.
- ADR-0002 to ADR-0007: direct `I2C_RDWR` backend over `x/sys`, identification
  before any write, per-chip locking, one-shot default, per-sensor errors,
  write allowlist enforced by test models. ADR-0001 waits for the hardware
  recon.
- Go module `github.com/JonasBorgesLM/vane` with a Go 1.26 floor.
- CI: build and race tests at the floor and on stable, macOS and Windows
  tests, cross-build for linux/arm64 without cgo, dependency and package
  boundary guards, fuzzing, lint, govulncheck, documentation and ADR checks,
  git-flow and Conventional Commit checks, API compatibility against the latest
  tag.
- Release workflow: signed-tag verification, source tarball with build
  provenance, API diff in the release notes.
- Process: CONTRIBUTING (git flow, commits, signed commits), RELEASING,
  SECURITY, pull request template, Dependabot.
