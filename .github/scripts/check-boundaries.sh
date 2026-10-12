#!/usr/bin/env bash
#
# Dependency and package-boundary guards.
#
# A file rather than inline YAML so it runs locally before a push:
#
#   ./.github/scripts/check-boundaries.sh
#
# Every check asks the Go resolver (`go list`), never greps go.mod: grepping a
# manifest matched a comment naming a dependency in another repository of this
# ecosystem and passed.
#
# The graph is evaluated for linux/arm64, the target platform. i2cdev's real
# backend sits behind `//go:build linux`, so on any other GOOS `go list` would
# not see its imports and check 1 would pass for the wrong reason.

set -uo pipefail
cd "$(dirname "$0")/../.."

export GOOS=linux GOARCH=arm64 CGO_ENABLED=0
MOD=$(go list -m)

FAILED=0
fail() { printf '\n\033[31mFAIL\033[0m  %s\n' "$1"; FAILED=1; }
pass() { printf '\033[32mok\033[0m    %s\n' "$1"; }
note() { printf '        %s\n' "$1"; }

# Each line: <package> <direct imports, including test imports>
imports=$(go list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}' ./...) || {
  fail "go list failed; the module does not resolve"; exit 1; }

# --- 1. Only i2cdev imports golang.org/x/sys (RNF-03) ----------------------
offenders=$(printf '%s\n' "$imports" | awk -v allow="$MOD/i2cdev" '
  $1 != allow { for (i = 2; i <= NF; i++) if ($i ~ /^golang\.org\/x\/sys(\/|$)/) print "  " $1 " -> " $i }')
if [ -n "$offenders" ]; then
  fail "golang.org/x/sys imported outside $MOD/i2cdev (RNF-03):"
  printf '%s\n' "$offenders"
  note "Everything that talks to the kernel goes through i2cdev; the rest compiles on any GOOS."
else
  pass "golang.org/x/sys is imported by i2cdev only"
fi

# --- 2. No external module but x/sys anywhere in the graph (RNF-03) --------
external=$(go list -deps -test -f '{{with .Module}}{{.Path}}{{end}}' ./... | sort -u |
  grep -vxF "$MOD" | grep -vxF 'golang.org/x/sys' || true)
if [ -n "$external" ]; then
  fail "modules outside the allowed set are in the build graph (RNF-03):"
  printf '  %s\n' $external
  note "A new dependency is a decision: an ADR first, then this list."
else
  pass "no external module besides golang.org/x/sys"
fi

# --- 3. The root package is a leaf (G-03) -----------------------------------
leaf=$(printf '%s\n' "$imports" | awk -v mod="$MOD" '
  $1 == mod { for (i = 2; i <= NF; i++) if (index($i, mod "/") == 1) print "  " $i }')
if [ -n "$leaf" ]; then
  fail "package $MOD imports a package of its own module:"
  printf '%s\n' "$leaf"
  note "The drivers import vane and sensehat imports the drivers; vane importing either is a cycle."
else
  pass "package vane imports no package of this module"
fi

# --- 4. vanetest's chip models do not import the drivers --------------------
#
# An oracle sharing the driver's register constants replicates the driver's
# mistakes, and the tests pass on a wrong address. Direct imports only:
# FakeBoard reaches chip/* transitively through sensehat, which shares no
# register constants.
oracle=$(printf '%s\n' "$imports" | awk -v mod="$MOD" '
  index($1, mod "/vanetest") == 1 { for (i = 2; i <= NF; i++) if (index($i, mod "/chip/") == 1) print "  " $1 " -> " $i }')
if [ -n "$oracle" ]; then
  fail "vanetest imports a chip driver:"
  printf '%s\n' "$oracle"
  note "Write the model from the datasheet; do not reuse the driver's constants."
else
  pass "vanetest imports no chip driver"
fi

# --- 5. Never I2C_SLAVE_FORCE (RF-07) ---------------------------------------
forced=$(grep -rnE --include='*.go' 'I2C_SLAVE_FORCE|\b0x0?706\b' . 2>/dev/null | grep -v '_test\.go:' || true)
if [ -n "$forced" ]; then
  fail "I2C_SLAVE_FORCE (0x0706) appears in non-test code (RF-07):"
  printf '%s\n' "$forced"
  note "Forcing the address fights a kernel driver for the same chip. Detect ownership and refuse instead."
else
  pass "no I2C_SLAVE_FORCE"
fi

echo
if [ "$FAILED" -ne 0 ]; then echo "Boundary checks failed."; exit 1; fi
echo "Boundary checks passed."
