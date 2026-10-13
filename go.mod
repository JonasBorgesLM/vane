module github.com/JonasBorgesLM/vane

// The floor is Go 1.26, the lowest viable version rather than the newest
// (RNF-09). It is set by golang.org/x/sys, the one dependency vane admits
// (RNF-03): x/sys v0.49.0 declares `go 1.26.0`, and 1.26 is also the oldest
// release still receiving security fixes, so pinning an older x/sys to reach a
// lower floor would only buy support for an unsupported toolchain. Raising this
// line is a CHANGELOG entry, never a side effect of `go get`.
go 1.26.0

require golang.org/x/sys v0.49.0
