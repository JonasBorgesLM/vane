package vanetest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/sensehat"
)

// ErrScriptEnded is what a FakeBoard's Environment returns when it is called
// more times than its script has steps. A consumer that polls more often than
// the test planned for finds out instead of being handed a repeated answer.
var ErrScriptEnded = errors.New("vanetest: the FakeBoard script has ended")

// The Sense HAT's fixed layout, repeated here from the sensehat package's probe
// table for the same reason the chip models repeat the register numbers.
const (
	boardBus      = "/dev/i2c-1"
	boardHTS221At = 0x5F
	boardLPS25HAt = 0x5C
)

func boardSource(chip vane.Chip) vane.Source {
	addr := uint16(boardHTS221At)
	if chip == vane.ChipLPS25H {
		addr = boardLPS25HAt
	}
	return vane.Source{Chip: chip, Bus: boardBus, Address: addr}
}

// Step is one scripted answer of a FakeBoard's Environment. Build one with
// Healthy, Result or Fail and refine it with Failing, Delayed, Times and
// Forever.
type Step struct {
	env   sensehat.Environment
	err   error
	delay time.Duration
	times int // 0 or 1: once; n: n times; -1: for ever
}

// Healthy is a step in which both chips deliver. at stamps every measurement;
// the temperatures are the HTS221's then the LPS25H's, as a Device returns them.
func Healthy(at time.Time, rh vane.RelativeHumidity, p vane.Hectopascal, tHTS221, tLPS25H vane.Celsius) Step {
	h, l := boardSource(vane.ChipHTS221), boardSource(vane.ChipLPS25H)
	return Step{env: sensehat.Environment{
		Humidity: sensehat.Reading[vane.RelativeHumidity]{Measurement: vane.Measurement[vane.RelativeHumidity]{Value: rh, Source: h, Time: at}},
		Pressure: sensehat.Reading[vane.Hectopascal]{Measurement: vane.Measurement[vane.Hectopascal]{Value: p, Source: l, Time: at}},
		Temperatures: []sensehat.Reading[vane.Celsius]{
			{Measurement: vane.Measurement[vane.Celsius]{Value: tHTS221, Source: h, Time: at}},
			{Measurement: vane.Measurement[vane.Celsius]{Value: tLPS25H, Source: l, Time: at}},
		},
	}}
}

// Result is a step that returns exactly env and err, for a scenario the other
// constructors do not describe, such as a reading carrying vane.FlagStale.
func Result(env sensehat.Environment, err error) Step { return Step{env: cloneEnv(env), err: err} }

// Fail is a step in which the call itself fails and no reading is returned, as
// when the context ends or the device is closed.
func Fail(err error) Step { return Step{err: err} }

// Failing makes chip's readings carry err, wrapped in a *vane.SensorError for
// that chip as a Device wraps them, and keeps the rest. When that leaves every
// reading failed, the step also returns the joined errors, because a Device
// does: a consumer scripted against a FakeBoard must see what it would see on
// the board.
func (s Step) Failing(chip vane.Chip, err error) Step {
	e := cloneEnv(s.env)
	src := boardSource(chip)
	fail := &vane.SensorError{Source: src, Op: "read", Err: err}
	switch chip {
	case vane.ChipHTS221:
		e.Humidity = sensehat.Reading[vane.RelativeHumidity]{Measurement: vane.Measurement[vane.RelativeHumidity]{Source: src}, Err: fail}
	case vane.ChipLPS25H:
		e.Pressure = sensehat.Reading[vane.Hectopascal]{Measurement: vane.Measurement[vane.Hectopascal]{Source: src}, Err: fail}
	case vane.ChipLSM9DS1, vane.ChipTCS3400:
		// Not part of the Environment: sensehat reads neither the IMU (ADR-0008)
		// nor the colour sensor (ADR-0009), so there is nothing of them to fail.
		return s
	}
	for i, t := range e.Temperatures {
		if t.Source.Chip == chip {
			e.Temperatures[i] = sensehat.Reading[vane.Celsius]{Measurement: vane.Measurement[vane.Celsius]{Source: src}, Err: fail}
		}
	}
	s.env = e
	if e.Humidity.Err != nil && e.Pressure.Err != nil {
		s.err = errors.Join(e.Humidity.Err, e.Pressure.Err)
	}
	return s
}

// Delayed makes the step take d, cut short by the context.
func (s Step) Delayed(d time.Duration) Step { s.delay = d; return s }

// Times repeats the step n times in a row.
func (s Step) Times(n int) Step { s.times = n; return s }

// Forever repeats the step for as long as the board is called.
func (s Step) Forever() Step { s.times = -1; return s }

// FakeBoard is a stand-in for a *sensehat.Device with the same method set, for a
// consumer such as beacon to test its adapter without hardware and without
// knowing a register. Environment answers from a script, one step per call.
//
// A test file in this package asserts that *sensehat.Device and *FakeBoard
// satisfy one interface, so the two cannot drift apart unnoticed.
type FakeBoard struct {
	mu      sync.Mutex
	script  []Step
	pos     int
	done    int // repetitions of script[pos] already given
	calls   int
	closed  bool
	sensors []sensehat.SensorInfo
	board   sensehat.BoardInfo
}

// NewFakeBoard returns a board that answers Environment with the steps in order.
// Its Sensors report both chips ready until SetSensors says otherwise.
func NewFakeBoard(script ...Step) *FakeBoard {
	return &FakeBoard{
		script: slices.Clone(script),
		sensors: []sensehat.SensorInfo{
			{Source: boardSource(vane.ChipHTS221), State: sensehat.StateReady},
			{Source: boardSource(vane.ChipLPS25H), State: sensehat.StateReady},
		},
	}
}

// Environment returns the next scripted answer. A closed board returns
// vane.ErrClosed. A context that has already ended returns its error and does
// not use up a step; one that ends during a step's delay does.
func (f *FakeBoard) Environment(ctx context.Context) (sensehat.Environment, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return sensehat.Environment{}, vane.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		f.mu.Unlock()
		return sensehat.Environment{}, err
	}
	f.calls++
	step, ok := f.next()
	f.mu.Unlock()
	if !ok {
		return sensehat.Environment{}, ErrScriptEnded
	}
	if step.delay > 0 {
		if err := sleep(ctx, step.delay); err != nil {
			return sensehat.Environment{}, err
		}
	}
	return cloneEnv(step.env), step.err
}

// next advances the script by one call. The caller holds f.mu.
func (f *FakeBoard) next() (Step, bool) {
	if f.pos >= len(f.script) {
		return Step{}, false
	}
	step := f.script[f.pos]
	f.done++
	if step.times >= 0 && f.done >= max(step.times, 1) {
		f.pos++
		f.done = 0
	}
	return step, true
}

// Sensors returns what the board reports for each chip. The result is a copy.
func (f *FakeBoard) Sensors() []sensehat.SensorInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sensors)
}

// SetSensors changes what Sensors reports, for a scenario in which a chip failed
// to open or is owned by the kernel.
func (f *FakeBoard) SetSensors(infos ...sensehat.SensorInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sensors = slices.Clone(infos)
}

// Board returns what SetBoard set; by default the board is absent.
func (f *FakeBoard) Board() sensehat.BoardInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.board
}

// SetBoard changes what Board reports.
func (f *FakeBoard) SetBoard(b sensehat.BoardInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.board = b
}

// Close ends the board. It is idempotent, as a Device's is.
func (f *FakeBoard) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// Closed reports whether Close has been called, for a test to assert that its
// subject released the board.
func (f *FakeBoard) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// Calls returns how many times Environment was called on an open board with a
// live context, counting a call that found the script ended.
func (f *FakeBoard) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// cloneEnv copies an Environment so that neither the script nor a caller can
// change the other through the Temperatures slice.
func cloneEnv(e sensehat.Environment) sensehat.Environment {
	e.Temperatures = slices.Clone(e.Temperatures)
	return e
}
