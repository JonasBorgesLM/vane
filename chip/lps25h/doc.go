// Package lps25h drives the ST LPS25H and LPS25HB pressure and temperature
// sensors over a vane.Bus.
//
// The two chips are one family here. They answer the same identity value and
// share every register this package uses, so a Sensor drives either without
// being told which; see Sensor.Variant for what that does and does not say.
//
// A Sensor identifies the chip before it writes anything, configures it with
// block data update on, and converts raw counts to hectopascals and degrees
// Celsius. By default it uses one-shot acquisition: each Read triggers a
// conversion, waits for it under the context and reads the result, so the
// sample is always new and the chip idles between reads.
//
// Continuous acquisition is available with WithMode: the chip converts at a
// fixed rate and Read returns its latest sample, with a quantity flagged
// vane.FlagStale when it is not newer than the previous one and
// vane.FlagOverrun when the chip says it was overwritten. The averaging
// register is left as the chip has it and there is no option for it: the sample
// counts per code are unconfirmed.
//
// A Sensor never retries. When a trigger goes unanswered it returns
// vane.ErrNotReady, and the next Read identifies and configures the chip again,
// which is how a chip that lost power recovers. What to do about the error in
// between is the caller's decision.
//
// The pressure is absolute, in hPa. The temperature is the chip's own and is not
// compensated for the heat of the Pi's CPU; that correction belongs to the
// consumer.
package lps25h
