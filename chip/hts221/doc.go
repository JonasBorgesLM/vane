// Package hts221 drives the ST HTS221 relative humidity and temperature sensor
// over a vane.Bus.
//
// A Sensor identifies the chip before it writes anything, reads and validates
// its factory calibration, configures it with block data update on, and then
// converts raw counts to percent relative humidity and degrees Celsius. By
// default it uses one-shot acquisition: each Read triggers a conversion, waits
// for it under the context and reads the result, so the sample is always new
// and the chip idles between reads.
//
// Continuous acquisition is available with WithMode: the chip converts at a
// fixed rate and Read returns its latest sample, flagged vane.FlagStale when it
// is not newer than the previous one. The chip has no overrun bit, so
// vane.FlagOverrun is never set.
//
// A Sensor never retries. When a trigger goes unanswered it returns
// vane.ErrNotReady, and the next Read identifies and configures the chip again,
// which is how a chip that lost power recovers. What to do about the error in
// between is the caller's decision.
//
// The temperature a Sensor reports is the chip's own. It is not compensated
// for the heat of the Pi's CPU, which raises it and lowers the humidity; that
// correction belongs to the consumer.
package hts221
