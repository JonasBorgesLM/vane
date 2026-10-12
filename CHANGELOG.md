# Changelog

Notable changes to vane. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). In
v0.x a minor version may break the API, and every such break is listed under
**Changed** with a ⚠️ marker.

The release workflow copies the section of the version being tagged into the
GitHub release, next to the API diff it generates, and refuses to publish a tag
with no section here.

## [Unreleased]

### Added

- Go module `github.com/JonasBorgesLM/vane` with a Go 1.26 floor.
- CI: build and race tests at the floor and on stable, macOS and Windows
  tests, cross-build for linux/arm64 without cgo, dependency and package
  boundary guards, fuzzing, lint, govulncheck, documentation and ADR checks,
  git-flow and Conventional Commit checks, API compatibility against the latest
  tag.
- Release workflow: signed-tag verification, source tarball with build
  provenance, API diff in the release notes.
