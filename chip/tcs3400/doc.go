// Package tcs3400 drives the ams TCS3400 colour sensor over a vane.Bus.
//
// A Sensor identifies the chip before it writes anything, then reads one
// integration per Read (ADR-0009): it turns the ADC on, waits for the cycle to
// complete, reads clear, red, green and blue in one transaction and turns the
// ADC off, so every sample is new and the chip sleeps between reads. A Read
// takes about one integration time, 178 ms by default.
//
// Values are raw counts (vane.Counts), carried with the gain and integration
// time they were taken with, so a caller can normalise them or apply its own
// lux coefficients: lux and colour temperature are derived quantities and
// depend on the optics in front of the sensor, which vane does not know. A
// channel at the configuration's maximum count, or a cycle the chip reports as
// saturated, is flagged vane.FlagClamped.
//
// The registers are 0x80 to 0xFF with bit 7 part of the address, so the package
// reads them through regio's byte-addressed scheme.
package tcs3400
