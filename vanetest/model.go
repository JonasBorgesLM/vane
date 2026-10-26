package vanetest

// ModelOption configures a chip model.
type ModelOption func(*modelOptions)

type modelOptions struct {
	rep       Reporter
	polls     int
	whoAmI    *uint8
	staggered bool
	addr      *uint16
}

// WithReporter sets where the model reports a protocol violation besides
// returning it. Pass the *testing.T of the test that uses the model.
func WithReporter(r Reporter) ModelOption { return func(o *modelOptions) { o.rep = r } }

// WithConversionPolls sets how many reads of the status register report "not
// ready" before a one-shot conversion completes. It stands for conversion time
// in a model with no clock, and keeps a test deterministic.
func WithConversionPolls(n int) ModelOption { return func(o *modelOptions) { o.polls = n } }

// WithWhoAmI makes the model answer its identity register with v, for tests of
// a driver meeting the wrong chip.
func WithWhoAmI(v uint8) ModelOption { return func(o *modelOptions) { o.whoAmI = &v } }

// WithModelAddress makes the model answer at addr instead of its default, for
// a chip that comes at more than one address, such as the TCS3400 (0x39 or
// 0x29 by variant).
func WithModelAddress(addr uint16) ModelOption { return func(o *modelOptions) { o.addr = &addr } }

func newModelOptions(opts []ModelOption) modelOptions {
	var o modelOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithStaggeredReady makes a conversion finish in two steps: the temperature is
// ready first and the humidity one read of the status register later. A driver
// that treats either data-ready bit as "the sample is ready" reads a humidity
// that is not there yet.
func WithStaggeredReady() ModelOption { return func(o *modelOptions) { o.staggered = true } }

// covers reports whether a transaction reading n registers from reg includes r.
func covers(reg, n, r int) bool { return r >= reg && r < reg+n }
