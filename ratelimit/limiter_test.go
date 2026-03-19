package ratelimit

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name  string
		rate  float64
		burst int
		clock config.Clock
	}{
		{name: "with fake clock", rate: 10, burst: 5, clock: config.NewFakeClock(time.Now())},
		{name: "with nil clock defaults to real", rate: 1, burst: 1, clock: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := New(tc.rate, tc.burst, tc.clock)
			assert.NotNil(t, l)
		})
	}
}

func TestAllow_UnderLimit(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	l := New(10, 5, fc)

	for i := 0; i < 5; i++ {
		err := l.Allow()
		require.NoError(t, err, "call %d should be allowed", i)
	}
}

func TestAllow_OverLimit(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	l := New(10, 3, fc)

	for i := 0; i < 3; i++ {
		err := l.Allow()
		require.NoError(t, err)
	}

	err := l.Allow()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestAllow_RefillAfterTime(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	l := New(1, 1, fc) // 1 token/sec, burst of 1

	err := l.Allow()
	require.NoError(t, err)

	err = l.Allow()
	require.Error(t, err)

	fc.Advance(2 * time.Second)

	err = l.Allow()
	require.NoError(t, err)
}

func TestAllow_BurstCap(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	l := New(100, 2, fc) // high rate, but burst of 2

	fc.Advance(10 * time.Second) // would accumulate 1000 tokens, but capped at 2

	err := l.Allow()
	require.NoError(t, err)

	err = l.Allow()
	require.NoError(t, err)

	err = l.Allow()
	require.Error(t, err)
}
