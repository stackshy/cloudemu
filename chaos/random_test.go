package chaos_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
)

// random.go's randFloat is unexported but is exercised by ProbabilisticFailure.
// These tests verify the distribution behaves reasonably via that public path.

// TestRandFloatDistribution checks that ProbabilisticFailure with p=0.5 hits
// roughly half the calls over a large sample. Tolerance is wide on purpose;
// the goal is to catch a stuck-at-0 or stuck-at-1 implementation.
func TestRandFloatDistributionRoughlyMatchesProbability(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("flaky")
	e.Apply(chaos.ProbabilisticFailure("storage", "Op", myErr, 0.5, time.Hour))

	const samples = 10000

	hits := 0

	for range samples {
		if eff := e.Check("storage", "Op"); eff.Error != nil {
			hits++
		}
	}

	// Expect ~5000 hits. Tolerance ±5% absolute (i.e. 4500–5500).
	const expected = samples / 2

	const tolerance = samples / 20

	if hits < expected-tolerance || hits > expected+tolerance {
		t.Errorf("hits=%d, expected %d±%d (p=0.5 over %d samples)",
			hits, expected, tolerance, samples)
	}
}

// TestRandFloatBoundsZeroProb confirms p=0.0 produces zero hits — guards
// against off-by-one bugs where the random source could produce 1.0.
func TestRandFloatBoundsZeroProb(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ProbabilisticFailure("storage", "Op", errors.New("x"), 0.0, time.Hour))

	for range 1000 {
		if eff := e.Check("storage", "Op"); eff.Error != nil {
			t.Fatalf("p=0.0 should never inject, got %v", eff.Error)
		}
	}
}

// TestRandFloatBoundsOneProb confirms p=1.0 produces all hits — guards
// against off-by-one bugs where the random source could produce 0.0 only.
func TestRandFloatBoundsOneProb(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ProbabilisticFailure("storage", "Op", errors.New("x"), 1.0, time.Hour))

	for range 1000 {
		if eff := e.Check("storage", "Op"); eff.Error == nil {
			t.Fatal("p=1.0 should always inject")
		}
	}
}
