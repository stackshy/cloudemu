// Package config provides configuration options for cloudemu services.
package config

import (
	"sync"
	"time"
)

// Clock provides time operations, enabling deterministic testing.
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the real system clock.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time { return time.Now() }

// Since returns the time elapsed since t.
func (RealClock) Since(t time.Time) time.Duration { return time.Since(t) }

// After waits for the duration to elapse and then sends the current time on the returned channel.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// FakeClock is a deterministic clock for testing.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock creates a FakeClock set to the given time.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the fake current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns the duration since t based on the fake clock.
func (c *FakeClock) Since(t time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(t)
}

// After returns a channel that receives the fake time immediately.
func (c *FakeClock) After(_ time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	ch <- c.now
	c.mu.Unlock()
	return ch
}

// Advance moves the fake clock forward by the given duration.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set sets the fake clock to a specific time.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
