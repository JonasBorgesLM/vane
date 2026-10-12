package poll_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/internal/poll"
)

// fakeClock advances virtual time and never really sleeps. Sleep honours the
// context the way the real clock does.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

const (
	interval = 5 * time.Millisecond
	timeout  = 20 * time.Millisecond
)

func TestReadyImmediatelyDoesNotSleep(t *testing.T) {
	clk := newClock()
	calls := 0
	err := poll.Until(context.Background(), clk, interval, timeout, func(context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if err != nil || calls != 1 || len(clk.sleeps) != 0 {
		t.Fatalf("err=%v calls=%d sleeps=%v, want nil, 1, none", err, calls, clk.sleeps)
	}
}

func TestReadyAfterSeveralPolls(t *testing.T) {
	clk := newClock()
	calls := 0
	err := poll.Until(context.Background(), clk, interval, timeout, func(context.Context) (bool, error) {
		calls++
		return calls == 3, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d, want nil, 3", err, calls)
	}
	if len(clk.sleeps) != 2 || clk.sleeps[0] != interval || clk.sleeps[1] != interval {
		t.Fatalf("sleeps = %v, want two intervals", clk.sleeps)
	}
}

// The last sleep is clamped to what remains, so the wait ends exactly at the
// deadline rather than a whole interval past it, and ready runs once more
// there.
func TestTimeoutIsExactAndPollsOnceAtTheDeadline(t *testing.T) {
	clk := newClock()
	start := clk.now
	calls := 0
	err := poll.Until(context.Background(), clk, 8*time.Millisecond, timeout, func(context.Context) (bool, error) {
		calls++
		return false, nil
	})
	if !errors.Is(err, vane.ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if elapsed := clk.now.Sub(start); elapsed != timeout {
		t.Fatalf("waited %v, want exactly %v", elapsed, timeout)
	}
	if calls != 4 {
		t.Fatalf("ready called %d times, want 4: at 0, 8 and 16 ms and at the 20 ms deadline", calls)
	}
}

func TestSlowReadyCountsAgainstTheTimeout(t *testing.T) {
	clk := newClock()
	calls := 0
	err := poll.Until(context.Background(), clk, interval, timeout, func(context.Context) (bool, error) {
		calls++
		clk.now = clk.now.Add(15 * time.Millisecond) // a transaction that takes 15 ms
		return false, nil
	})
	if !errors.Is(err, vane.ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if calls != 2 {
		t.Fatalf("ready called %d times, want 2: the second call already passes the 20 ms deadline", calls)
	}
}

func TestReadyErrorStopsAtOnce(t *testing.T) {
	boom := errors.New("boom")
	clk := newClock()
	calls := 0
	err := poll.Until(context.Background(), clk, interval, timeout, func(context.Context) (bool, error) {
		calls++
		return false, boom
	})
	if !errors.Is(err, boom) || calls != 1 || len(clk.sleeps) != 0 {
		t.Fatalf("err=%v calls=%d sleeps=%v, want boom, 1, none", err, calls, clk.sleeps)
	}
}

// Negative control, seen failing: deleting the ctx.Err() check at the top of
// the loop makes ready run once on a context that was already cancelled.
func TestCancelledBeforeStartNeverCallsReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := poll.Until(ctx, newClock(), interval, timeout, func(context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("err=%v calls=%d, want context.Canceled and no call", err, calls)
	}
}

// A context that ends during ready (a transaction in flight cannot be
// interrupted) costs that one call and no more: Until does not sleep or call
// again. This is the latency bound RNF-05 documents.
func TestCancelDuringReadyCostsOneCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clk := newClock()
	calls := 0
	err := poll.Until(ctx, clk, interval, timeout, func(context.Context) (bool, error) {
		calls++
		cancel()
		return false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 || len(clk.sleeps) != 0 {
		t.Fatalf("calls=%d sleeps=%v, want 1 call and no sleep after the cancel", calls, clk.sleeps)
	}
}

func TestInvalidDurationsAreRefused(t *testing.T) {
	ready := func(context.Context) (bool, error) { t.Fatal("ready called"); return true, nil }
	for _, c := range []struct{ interval, timeout time.Duration }{{0, timeout}, {interval, 0}, {-1, timeout}, {interval, -1}} {
		if err := poll.Until(context.Background(), newClock(), c.interval, c.timeout, ready); err == nil {
			t.Errorf("interval=%v timeout=%v accepted", c.interval, c.timeout)
		}
	}
}

func TestNilClockMeansRealAndWorks(t *testing.T) {
	calls := 0
	err := poll.Until(context.Background(), nil, time.Millisecond, time.Second, func(context.Context) (bool, error) {
		calls++
		return calls == 3, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d, want nil, 3", err, calls)
	}
}

// The real clock wakes on cancellation instead of finishing its interval, and
// leaves no goroutine or timer behind (RNF-05).
func TestRealSleepWakesOnCancelAndLeaksNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		start := time.Now()
		err := poll.Until(ctx, poll.Real, time.Hour, 2*time.Hour, func(context.Context) (bool, error) { return false, nil })
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("returned after %v, the hour-long interval was not interrupted", d)
		}
	}
	var after int
	for range 50 { // let finished goroutines be reaped
		if after = runtime.NumGoroutine(); after <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutines: %d before, %d after", before, after)
}
