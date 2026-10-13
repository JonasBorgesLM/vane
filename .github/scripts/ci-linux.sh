#!/usr/bin/env bash
#
# Runs the checks the pipeline runs on Linux, in a Linux container, for anyone
# developing on another operating system.
#
#   ./.github/scripts/ci-linux.sh
#
# Why it exists. gosec and golangci-lint skip files behind a `//go:build linux`
# tag when they run on a Mac, so i2cdev passes both there and fails in CI; the
# tests that need Linux cannot run at all; and a golangci-lint built with the
# Go version CI pins cannot type-check a newer local standard library for
# Linux. All three were met writing i2cdev. Running as an ordinary user, not
# root, is deliberate: the permission tests skip for root.
#
# Needs docker. The tool versions are read from ci.yml so the two cannot drift.

set -euo pipefail
cd "$(dirname "$0")/../.." || exit 1

workflow=.github/workflows/ci.yml
pin() { awk -v k="$1" '$1 == k":" { gsub(/"/, "", $2); print $2; exit }' "$workflow"; }
go_version=$(pin GO_VERSION)
lint_version=$(pin LINT_VERSION)
gosec_version=$(pin GOSEC_VERSION)
for v in "$go_version" "$lint_version" "$gosec_version"; do
  [ -n "$v" ] || { echo "could not read the tool pins from $workflow" >&2; exit 1; }
done

cache="${VANE_CI_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/vane-ci}"
mkdir -p "$cache/gocache" "$cache/gopath"
chmod -R a+rwX "$cache"

docker run --rm --user "$(id -u):$(id -g)" \
  -v "$PWD:/src:ro" -v "$cache/gocache:/tmp/gocache" -v "$cache/gopath:/tmp/gopath" -w /src \
  -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath -e GOFLAGS=-mod=readonly \
  -e PATH=/tmp/gopath/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
  -e LINT_VERSION="$lint_version" -e GOSEC_VERSION="$gosec_version" \
  "golang:$go_version" sh -c '
    set -e
    [ -x /tmp/gopath/bin/golangci-lint ] && /tmp/gopath/bin/golangci-lint version 2>&1 | grep -q "${LINT_VERSION#v}" ||
      GOFLAGS= go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$LINT_VERSION"
    [ -x /tmp/gopath/bin/gosec ] ||
      GOFLAGS= go install "github.com/securego/gosec/v2/cmd/gosec@$GOSEC_VERSION"
    echo "--- go version";        go version
    echo "--- gofmt";             test -z "$(gofmt -l .)" && echo ok
    echo "--- go vet";            go vet ./... && echo ok
    echo "--- golangci-lint";     golangci-lint run ./...
    echo "--- gosec -tests";      gosec -tests -exclude-generated -quiet ./... && echo ok
    echo "--- go test -race";     go test -race -count=1 ./...
  '
