// Package poll waits for a condition, such as a chip's data-ready bit, while
// honouring a context.
//
// Cancellation is not instantaneous: an I2C ioctl in flight cannot be
// interrupted. Until checks the context before every call to ready and sleeps
// in a way that wakes on cancellation, so the worst-case latency is one call to
// ready plus the part of an interval already slept (RNF-05).
package poll

import (
	"context"
	"errors"
	"time"

	"github.com/JonasBorgesLM/vane"
)

// Clock is what Until needs from time. Production code uses Real; tests inject
// a fake that advances virtual time, so they do not sleep.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep blocks for d, or returns ctx.Err() as soon as ctx ends.
	Sleep(ctx context.Context, d time.Duration) error
}

// Real is the Clock backed by the wall clock. Each Sleep makes a timer; a
// driver that waits on every read uses Reusable instead.
var Real Clock = realClock{}

// Reusable returns a Clock on the wall clock whose Sleep reuses one timer, so
// waiting allocates nothing (RNF-08). It is not safe for concurrent use: the
// caller serialises it, as a driver does with its per-chip mutex.
func Reusable() Clock { return &reusableClock{} }

type realClock struct{}

// Now returns the wall-clock time.
func (realClock) Now() time.Time { return time.Now() }

// Sleep waits d, or until ctx ends, and stops its timer either way.
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type reusableClock struct{ t *time.Timer }

// Now returns the wall-clock time.
func (*reusableClock) Now() time.Time { return time.Now() }

// Sleep waits d, or until ctx ends. Since Go 1.23 a stopped or reset timer has
// no stale value to drain, which is what lets one timer serve every call.
func (c *reusableClock) Sleep(ctx context.Context, d time.Duration) error {
	if c.t == nil {
		c.t = time.NewTimer(d)
	} else {
		c.t.Reset(d)
	}
	select {
	case <-c.t.C:
		return nil
	case <-ctx.Done():
		c.t.Stop()
		return ctx.Err()
	}
}

// Until calls ready immediately and then every interval until it reports true,
// returns an error, ctx ends or timeout elapses.
//
//   - ready reports true: Until returns nil.
//   - ready returns an error: Until returns it at once and does not call again.
//   - ctx ends: Until returns ctx.Err(), without calling ready again.
//   - timeout elapses: Until returns vane.ErrNotReady. ready is called one last
//     time at the deadline, so a sample that arrives on the last interval is
//     not lost.
//
// clk may be nil, meaning Real. interval and timeout must be positive.
func Until(ctx context.Context, clk Clock, interval, timeout time.Duration, ready func(context.Context) (bool, error)) error {
	if interval <= 0 || timeout <= 0 {
		return errors.New("poll: interval and timeout must be positive")
	}
	if clk == nil {
		clk = Real
	}
	start := clk.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := ready(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		remaining := timeout - clk.Now().Sub(start)
		if remaining <= 0 {
			return vane.ErrNotReady
		}
		if err := clk.Sleep(ctx, min(interval, remaining)); err != nil {
			return err
		}
	}
}
