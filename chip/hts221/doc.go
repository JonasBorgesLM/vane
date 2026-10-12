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
// The temperature a Sensor reports is the chip's own. It is not compensated
// for the heat of the Pi's CPU, which raises it and lowers the humidity; that
// correction belongs to the consumer.
package hts221
