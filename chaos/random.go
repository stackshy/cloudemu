package chaos

import (
	"crypto/rand"
	"encoding/binary"
)

// randFloat returns a float in [0.0, 1.0). Uses crypto/rand to avoid the
// global math/rand mutex and to keep behavior reproducible-ish in tests
// (each call is independent).
func randFloat() float64 {
	var b [8]byte

	_, _ = rand.Read(b[:])

	const mantissaBits = 53

	const shift = 64 - mantissaBits

	const denom = 1 << mantissaBits

	v := binary.BigEndian.Uint64(b[:]) >> shift

	return float64(v) / float64(denom)
}
