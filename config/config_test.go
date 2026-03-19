package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealClock_Now(t *testing.T) {
	c := RealClock{}
	before := time.Now()
	now := c.Now()
	after := time.Now()

	assert.False(t, now.Before(before))
	assert.False(t, now.After(after))
}

func TestRealClock_Since(t *testing.T) {
	c := RealClock{}
	past := time.Now().Add(-1 * time.Second)

	d := c.Since(past)
	assert.GreaterOrEqual(t, d.Seconds(), float64(1))
}

func TestRealClock_After(t *testing.T) {
	c := RealClock{}
	ch := c.After(1 * time.Millisecond)

	select {
	case tm := <-ch:
		assert.False(t, tm.IsZero())
	case <-time.After(1 * time.Second):
		t.Fatal("RealClock.After did not fire within timeout")
	}
}

func TestFakeClock_Now(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	fc := NewFakeClock(fixed)

	assert.Equal(t, fixed, fc.Now())
}

func TestFakeClock_Since(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	fc := NewFakeClock(fixed)

	past := fixed.Add(-5 * time.Minute)
	assert.Equal(t, 5*time.Minute, fc.Since(past))
}

func TestFakeClock_After(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	fc := NewFakeClock(fixed)

	ch := fc.After(999 * time.Hour)

	select {
	case tm := <-ch:
		assert.Equal(t, fixed, tm)
	default:
		t.Fatal("FakeClock.After should return immediately")
	}
}

func TestFakeClock_Advance(t *testing.T) {
	fixed := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(fixed)

	fc.Advance(10 * time.Minute)
	assert.Equal(t, fixed.Add(10*time.Minute), fc.Now())

	fc.Advance(5 * time.Second)
	assert.Equal(t, fixed.Add(10*time.Minute+5*time.Second), fc.Now())
}

func TestFakeClock_Set(t *testing.T) {
	fc := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	newTime := time.Date(2030, 12, 31, 23, 59, 59, 0, time.UTC)
	fc.Set(newTime)
	assert.Equal(t, newTime, fc.Now())
}

func TestNewOptions_Defaults(t *testing.T) {
	opts := NewOptions()

	assert.Equal(t, "us-east-1", opts.Region)
	assert.Equal(t, "123456789012", opts.AccountID)
	assert.Equal(t, "mock-project", opts.ProjectID)
	assert.Equal(t, time.Duration(0), opts.Latency)
	require.NotNil(t, opts.Clock)
	_, isReal := opts.Clock.(RealClock)
	assert.True(t, isReal)
}

func TestWithClock(t *testing.T) {
	fc := NewFakeClock(time.Now())
	opts := NewOptions(WithClock(fc))

	assert.Equal(t, fc, opts.Clock)
}

func TestWithRegion(t *testing.T) {
	tests := []struct {
		name   string
		region string
	}{
		{name: "eu-west-1", region: "eu-west-1"},
		{name: "empty string", region: ""},
		{name: "ap-southeast-1", region: "ap-southeast-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := NewOptions(WithRegion(tc.region))
			assert.Equal(t, tc.region, opts.Region)
		})
	}
}

func TestWithLatency(t *testing.T) {
	opts := NewOptions(WithLatency(50 * time.Millisecond))
	assert.Equal(t, 50*time.Millisecond, opts.Latency)
}

func TestWithAccountID(t *testing.T) {
	opts := NewOptions(WithAccountID("999999999999"))
	assert.Equal(t, "999999999999", opts.AccountID)
}

func TestWithProjectID(t *testing.T) {
	opts := NewOptions(WithProjectID("my-gcp-project"))
	assert.Equal(t, "my-gcp-project", opts.ProjectID)
}

func TestMultipleOptions(t *testing.T) {
	fc := NewFakeClock(time.Now())
	opts := NewOptions(
		WithClock(fc),
		WithRegion("eu-central-1"),
		WithLatency(100*time.Millisecond),
		WithAccountID("abc"),
		WithProjectID("xyz"),
	)

	assert.Equal(t, fc, opts.Clock)
	assert.Equal(t, "eu-central-1", opts.Region)
	assert.Equal(t, 100*time.Millisecond, opts.Latency)
	assert.Equal(t, "abc", opts.AccountID)
	assert.Equal(t, "xyz", opts.ProjectID)
}
