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
// The pressure is absolute, in hPa. The temperature is the chip's own and is not
// compensated for the heat of the Pi's CPU; that correction belongs to the
// consumer.
package lps25h
