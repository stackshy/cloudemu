//go:build !race

package vtl

// raceEnabled reports a -race build, where sync.Pool drops items at random and
// allocation counts are not meaningful.
const raceEnabled = false
