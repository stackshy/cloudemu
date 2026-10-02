//go:build race

package cloudwatch

// raceEnabled is true when the tests run with -race, which is several times
// slower, so time limits are relaxed.
const raceEnabled = true
