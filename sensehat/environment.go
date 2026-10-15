package sensehat

import (
	"context"
	"errors"

	"github.com/JonasBorgesLM/vane"
)

// Reading pairs a measurement with the error of taking it. Err == nil means the
// Measurement is valid; otherwise it holds only the Source, so the caller still
// knows which chip failed, and Err is usually a *vane.SensorError.
type Reading[T ~float64] struct {
	vane.Measurement[T]
	Err error
}

// Environment is one reading of everything the board measures. Each field is
// its own Reading: one chip failing leaves the others valid (RF-05).
type Environment struct {
	// Humidity is the HTS221's. Pressure is the LPS25H's.
	Humidity Reading[vane.RelativeHumidity]
	Pressure Reading[vane.Hectopascal]
	// Temperatures has one reading per requested chip, HTS221 first, each the
	// chip's own and not compensated for the heat of the Pi (RI-03). A chip that
	// could not be read has an entry whose Err says so.
	Temperatures []Reading[vane.Celsius]
}

// Environment reads every requested chip, one after the other.
//
// A chip that is not ready, or whose read fails, gives readings carrying the
// error; the others are unaffected. The returned error is not nil only if the
// context ended, the Device is closed, or every requested chip failed, in which
// case it joins their errors. A chip that was not requested has Err set to
// ErrNotRequested on its main field and no entry in Temperatures.
func (d *Device) Environment(ctx context.Context) (Environment, error) {
	if d.isClosed() {
		return Environment{}, vane.ErrClosed
	}
	var env Environment
	var failed []error
	delivered := 0
	for _, s := range d.slots {
		if s.info.State == StateNotRequested {
			s.a.absent(&env)
			continue
		}
		// A context that has ended needs no check here: the drivers' first transaction
		// does it, as the vane.Bus contract requires, and puts the error in the
		// reading.
		if rerr := s.a.fill(ctx, &env, s.info.Err); rerr != nil {
			failed = append(failed, rerr)
		} else {
			delivered++
		}
	}
	if err := ctx.Err(); err != nil {
		return env, err
	}
	if delivered == 0 {
		return env, errors.Join(failed...)
	}
	return env, nil
}
