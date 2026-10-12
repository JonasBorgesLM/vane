package hts221_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/JonasBorgesLM/vane/vanetest"
)

var ctx = context.Background()

// rig is a driver's test bench: a model on a fake bus, and a reporter that
// fails the test if the driver breaks a protocol rule.
type rig struct {
	t     *testing.T
	model *vanetest.HTS221Model
	bus   *vanetest.FakeBus
	rep   *reporter
}

type reporter struct{ t *testing.T }

func (r *reporter) Errorf(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf("protocol violation: "+format, args...)
}

func newRig(t *testing.T, opts ...vanetest.ModelOption) *rig {
	t.Helper()
	rep := &reporter{t: t}
	m := vanetest.NewHTS221(append([]vanetest.ModelOption{vanetest.WithReporter(rep)}, opts...)...)
	return &rig{t: t, model: m, bus: vanetest.NewFakeBus(m), rep: rep}
}

// writes returns the transactions that wrote data and read nothing: the
// configuration and trigger writes, not the address phase of a read.
func writes(bus *vanetest.FakeBus) []vanetest.Tx {
	var out []vanetest.Tx
	for _, tx := range bus.Log() {
		if len(tx.W) > 0 && len(tx.R) == 0 && tx.Err == nil {
			out = append(out, tx)
		}
	}
	return out
}

func describe(log []vanetest.Tx) string {
	var s string
	for i, tx := range log {
		s += fmt.Sprintf("  %d: addr=%#x w=%x r=%x err=%v\n", i, tx.Addr, tx.W, tx.R, tx.Err)
	}
	return s
}
