// Package inject provides error injection for testing cloudemu services.
package inject

import (
	"math/rand"
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

// ShouldInject returns true with the configured probability.
func (p *Probabilistic) ShouldInject() bool {
	return rand.Float64() < p.Probability
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
