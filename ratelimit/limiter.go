// Package ratelimit provides token bucket rate limiting for cloudemu services.
package ratelimit

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
)

// Limiter implements a token bucket rate limiter.
type Limiter struct {
	mu         sync.Mutex
	rate       float64 // tokens per second
	burst      int     // max tokens
	tokens     float64
	lastRefill time.Time
	clock      config.Clock
}

// New creates a new Limiter with the given rate (requests per second) and burst size.
func New(rate float64, burst int, clock config.Clock) *Limiter {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &Limiter{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst),
		lastRefill: clock.Now(),
		clock:      clock,
	}
}

// Allow checks if a request is allowed. Returns a Throttled error if rate limited.
func (l *Limiter) Allow() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()

	l.tokens += elapsed * l.rate
	if l.tokens > float64(l.burst) {
		l.tokens = float64(l.burst)
	}

	l.lastRefill = now

	if l.tokens < 1 {
		return cerrors.New(cerrors.Throttled, "rate limit exceeded")
	}

	l.tokens--

	return nil
}
