// Package vanetest provides test doubles for code that talks to vane.Bus: a
// register-level fake bus with fault injection, building blocks for chip
// models, and recording and replay of bus transactions.
//
// Chip models are written from the datasheet and must not import the drivers
// they test. A model that shares the driver's register constants replicates the
// driver's mistakes, and the tests pass on a wrong address.
package vanetest
