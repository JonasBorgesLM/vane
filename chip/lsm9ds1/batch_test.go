package lsm9ds1_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lsm9ds1"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// fill puts n samples in the FIFO, sample i with gyroscope X = base+i, so the
// order can be read back.
func fill(r *rig, base int16, n int) {
	for i := range n {
		g := base + int16(i) // #nosec G115 -- n is small
		r.ag.Set(vanetest.Axes{g, 0, 0}, vanetest.Axes{-g, 0, 0})
		r.ag.Tick()
	}
}

func gyroX(m lsm9ds1.Motion) int32 { return m.AngularRate.X.Raw }

func TestEachModeRefusesTheOtherMethod(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r)
	n := len(r.bus.Log())
	if _, err := s.ReadBatch(bg, make([]lsm9ds1.Motion, 4)); !errors.Is(err, lsm9ds1.ErrWrongMode) {
		t.Errorf("ReadBatch without WithFIFO: %v, want ErrWrongMode", err)
	}
	r2 := newRig(t, nil, nil)
	f := newSensor(t, r2, lsm9ds1.WithFIFO())
	n2 := len(r2.bus.Log())
	if _, err := f.Read(bg); !errors.Is(err, lsm9ds1.ErrWrongMode) {
		t.Errorf("Read WithFIFO: %v, want ErrWrongMode", err)
	}
	if len(r.bus.Log()) != n || len(r2.bus.Log()) != n2 {
		t.Error("a refused call reached the bus")
	}
	r2.mag.Tick()
	if _, err := f.ReadMagnetic(bg); err != nil {
		t.Errorf("ReadMagnetic WithFIFO: %v, want it to work", err)
	}
}

func TestWithFIFOConfiguresContinuousMode(t *testing.T) {
	r := newRig(t, nil, nil)
	newSensor(t, r, lsm9ds1.WithFIFO())
	if got := r.ag.Peek(0x23); got != 0x02 {
		t.Errorf("CTRL_REG9 = %#x, want FIFO_EN", got)
	}
	if got := r.ag.Peek(0x2E); got != 6<<5 {
		t.Errorf("FIFO_CTRL = %#x, want Continuous (110)", got)
	}
}

// The first sample after entering FIFO mode is dropped (DocID025715 Rev 3
// p. 23); the rest come oldest first. Negative control: without the discard the
// first sample returned is 100, not 101.
func TestReadBatchDropsTheFirstSampleThenReturnsOldestFirst(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r, lsm9ds1.WithFIFO())
	fill(r, 100, 5)
	dst := make([]lsm9ds1.Motion, 10)
	n, err := s.ReadBatch(bg, dst)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v, want 4 (five held, the first dropped)", n, err)
	}
	for i := range n {
		if got := gyroX(dst[i]); got != int32(101+i) {
			t.Errorf("sample %d: gyro X raw %d, want %d", i, got, 101+i)
		}
		if got := dst[i].Acceleration.X.Raw; got != -int32(101+i) {
			t.Errorf("sample %d: accel X raw %d, want %d", i, got, -(101 + i))
		}
	}
	fill(r, 200, 3)
	if n, err := s.ReadBatch(bg, dst); err != nil || n != 3 || gyroX(dst[0]) != 200 {
		t.Fatalf("second batch: n=%d err=%v first=%d, want 3 from 200: only the first batch drops one", n, err, gyroX(dst[0]))
	}
}

func TestReadBatchTakesNoMoreThanDstHolds(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r, lsm9ds1.WithFIFO())
	fill(r, 0, 11) // ten after the discard
	dst := make([]lsm9ds1.Motion, 4)
	if n, err := s.ReadBatch(bg, dst); err != nil || n != 4 || gyroX(dst[3]) != 4 {
		t.Fatalf("n=%d err=%v last=%d, want 4 ending at 4", n, err, gyroX(dst[3]))
	}
	if got := r.ag.Buffered(); got != 6 {
		t.Errorf("%d left in the FIFO, want 6", got)
	}
	if n, _ := s.ReadBatch(bg, nil); n != 0 {
		t.Errorf("an empty dst returned %d", n)
	}
}

// A full FIFO is drained in more than one burst (8 samples each).
func TestReadBatchDrainsAFullFIFO(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r, lsm9ds1.WithFIFO())
	fill(r, 0, 1)
	_, _ = s.ReadBatch(bg, make([]lsm9ds1.Motion, 1)) // spend the discard
	fill(r, 1000, 32)
	dst := make([]lsm9ds1.Motion, 40)
	n, err := s.ReadBatch(bg, dst)
	if err != nil || n != 32 {
		t.Fatalf("n=%d err=%v, want 32", n, err)
	}
	for i := range n {
		if gyroX(dst[i]) != int32(1000+i) {
			t.Fatalf("sample %d = %d, want %d", i, gyroX(dst[i]), 1000+i)
		}
	}
	if got := r.ag.Buffered(); got != 0 {
		t.Errorf("%d left, want the FIFO empty", got)
	}
}

func TestReadBatchFlagsTheFirstSampleAfterAnOverrun(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r, lsm9ds1.WithFIFO())
	fill(r, 0, 40) // eight overwritten
	dst := make([]lsm9ds1.Motion, 40)
	n, err := s.ReadBatch(bg, dst)
	if err != nil || n != 31 {
		t.Fatalf("n=%d err=%v, want 31 (32 held, one dropped)", n, err)
	}
	if !dst[0].AngularRate.X.Flags.Has(vane.FlagOverrun) || !dst[0].Acceleration.Z.Flags.Has(vane.FlagOverrun) {
		t.Errorf("first sample flags %v, want FlagOverrun", dst[0].AngularRate.X.Flags)
	}
	if dst[1].AngularRate.X.Flags.Has(vane.FlagOverrun) {
		t.Errorf("second sample flagged too: %v", dst[1].AngularRate.X.Flags)
	}
	fill(r, 0, 2)
	if _, _ = s.ReadBatch(bg, dst); dst[0].AngularRate.X.Flags.Has(vane.FlagOverrun) {
		t.Error("the overrun was reported again")
	}
}

func TestReadBatchStampsSamplesOnePeriodApart(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r, lsm9ds1.WithFIFO(), lsm9ds1.WithRate(lsm9ds1.Rate119Hz))
	fill(r, 0, 4)
	dst := make([]lsm9ds1.Motion, 4)
	n, _ := s.ReadBatch(bg, dst)
	second := float64(time.Second)
	period := time.Duration(second / 119)
	if n != 3 || !dst[2].AngularRate.X.Time.Equal(fixedTime) {
		t.Fatalf("n=%d last time %v, want 3 ending at %v", n, dst[2].AngularRate.X.Time, fixedTime)
	}
	for i := range n - 1 {
		if d := dst[i+1].AngularRate.X.Time.Sub(dst[i].AngularRate.X.Time); d != period {
			t.Errorf("samples %d and %d are %v apart, want %v", i, i+1, d, period)
		}
	}
}

func TestReadBatchStopsWhenTheContextEnds(t *testing.T) {
	r := newRig(t, nil, nil)
	s := newSensor(t, r, lsm9ds1.WithFIFO())
	fill(r, 0, 30)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if n, err := s.ReadBatch(ctx, make([]lsm9ds1.Motion, 30)); !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatalf("n=%d err=%v, want 0 and context.Canceled", n, err)
	}
}

func TestReadBatchDoesNotAllocate(t *testing.T) {
	b := directBus{vanetest.NewLSM9DS1AccelGyro(), vanetest.NewLSM9DS1Magnetometer()}
	s, err := lsm9ds1.New(bg, b, lsm9ds1.WithFIFO())
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]lsm9ds1.Motion, 32)
	if n := testing.AllocsPerRun(50, func() {
		for range 20 {
			b.ag.Tick()
		}
		_, _ = s.ReadBatch(bg, dst)
	}); n != 0 {
		t.Errorf("ReadBatch allocates %v times per call, want 0", n)
	}
}
