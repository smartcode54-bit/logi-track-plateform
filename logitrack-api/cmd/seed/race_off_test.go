//go:build !race

package main

// raceEnabled: timing budgets are not asserted under the race detector, which slows hashing and encoding.
const raceEnabled = false
