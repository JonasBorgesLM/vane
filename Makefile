# Developer shortcuts. CI does not use this file: it calls the same commands
# directly, so a change here cannot change what CI checks.

SHELL := /bin/bash

.PHONY: test ci-linux hw-test

test:
	go test -race ./...

# What CI runs on Linux (lint, gosec, tests), in a container. Needed on macOS and
# Windows, where Linux-only code is not compiled.
ci-linux:
	./.github/scripts/ci-linux.sh

# Run the hardware suite on a Raspberry Pi over SSH. The test binary is built
# here for linux/arm64, copied over and run there, and its output is kept in
# hw-results/ (not committed) as the evidence for docs/hardware.md.
#
#	make hw-test HW_HOST=pi@raspberrypi.local HW_EXPECT=custom
#
# HW_EXPECT is "custom" (board set up for vane) or "default" (stock overlay).
# Leave it empty to run what can be run and skip the rest.
HW_HOST   ?=
HW_BUS    ?= /dev/i2c-1
HW_EXPECT ?=
HW_BIN    := hw-results/conformance.test

hw-test:
	@test -n "$(HW_HOST)" || { echo "set HW_HOST, e.g. make hw-test HW_HOST=pi@raspberrypi.local" >&2; exit 2; }
	@mkdir -p hw-results
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags hardware -o $(HW_BIN) ./internal/conformance
	scp -q $(HW_BIN) $(HW_HOST):/tmp/vane-conformance.test
	ssh $(HW_HOST) 'VANE_I2C_BUS=$(HW_BUS) VANE_EXPECT=$(HW_EXPECT) /tmp/vane-conformance.test -test.v -test.run Hardware; s=$$?; rm -f /tmp/vane-conformance.test; exit $$s' \
		2>&1 | tee hw-results/$$(date -u +%Y%m%dT%H%M%SZ)-$(HW_EXPECT).txt; exit $${PIPESTATUS[0]}
