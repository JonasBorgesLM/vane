package vanetest

// ModelOption configures a chip model.
type ModelOption func(*modelOptions)

type modelOptions struct {
	rep    Reporter
	polls  int
	whoAmI *uint8
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

func newModelOptions(opts []ModelOption) modelOptions {
	var o modelOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
