// Package inject provides error injection for testing cloudemu services.
package inject

import (
	"crypto/rand"
	"encoding/binary"
	"math"
	"sync/atomic"
)

// Policy determines when to inject an error.
type Policy interface {
	ShouldInject() bool
}

// Always injects an error on every call.
type Always struct{}

// ShouldInject always returns true.
func (Always) ShouldInject() bool { return true }

// NthCall injects an error on every Nth call.
type NthCall struct {
	N       int
	counter int64
}

// NewNthCall creates a policy that injects on every Nth call.
func NewNthCall(n int) *NthCall {
	return &NthCall{N: n}
}

// ShouldInject returns true on every Nth call.
func (p *NthCall) ShouldInject() bool {
	c := atomic.AddInt64(&p.counter, 1)
	return int(c)%p.N == 0
}

// Probabilistic injects errors with a given probability.
type Probabilistic struct {
	Probability float64
}

// NewProbabilistic creates a policy with the given probability (0.0-1.0).
func NewProbabilistic(p float64) *Probabilistic {
	return &Probabilistic{Probability: p}
}

// float64MantissaBits is the number of bits in a float64 mantissa (IEEE 754).
const float64MantissaBits = 53

// float64ShiftBits is the number of bits to discard from a uint64 to get 53 bits.
const float64ShiftBits = 64 - float64MantissaBits

// cryptoRandFloat64 returns a cryptographically secure random float64 in [0.0, 1.0).
func cryptoRandFloat64() float64 {
	var b [8]byte

	_, _ = rand.Read(b[:])

	return float64(binary.LittleEndian.Uint64(b[:])>>float64ShiftBits) / math.Exp2(float64MantissaBits)
}

// ShouldInject returns true with the configured probability.
func (p *Probabilistic) ShouldInject() bool {
	return cryptoRandFloat64() < p.Probability
}

// Countdown injects errors for the first N calls, then stops.
type Countdown struct {
	remaining int64
}

// NewCountdown creates a policy that injects for the first n calls.
func NewCountdown(n int) *Countdown {
	return &Countdown{remaining: int64(n)}
}

// ShouldInject returns true while the countdown is positive.
func (p *Countdown) ShouldInject() bool {
	r := atomic.AddInt64(&p.remaining, -1)
	return r >= 0
}
