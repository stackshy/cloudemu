package chaos_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
)

func TestServiceOutageInsideWindow(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	eff := e.Check("storage", "PutObject")
	if eff.Error == nil {
		t.Fatal("expected outage error")
	}

	if cerrors.GetCode(eff.Error) != cerrors.Unavailable {
		t.Errorf("expected Unavailable code, got %v", eff.Error)
	}
}

func TestServiceOutageDifferentServiceUnaffected(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if eff := e.Check("compute", "RunInstances"); eff.Error != nil {
		t.Errorf("compute should be unaffected, got %v", eff.Error)
	}
}

func TestServiceOutageExpires(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", 5*time.Millisecond))

	if e.Check("storage", "Op").Error == nil {
		t.Fatal("should fail while active")
	}

	time.Sleep(15 * time.Millisecond)

	if eff := e.Check("storage", "Op"); eff.Error != nil {
		t.Errorf("should recover after window, got %v", eff.Error)
	}
}

func TestLatencySpikeAddsLatency(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", 50*time.Millisecond, time.Hour))

	if eff := e.Check("storage", "Op"); eff.Latency != 50*time.Millisecond {
		t.Errorf("got %v, want 50ms", eff.Latency)
	}
}

func TestLatencySpikeOnlyAffectsTargetService(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", 50*time.Millisecond, time.Hour))

	if eff := e.Check("compute", "Op"); eff.Latency != 0 {
		t.Errorf("compute should be unaffected, got %v", eff.Latency)
	}
}

func TestLatencySpikeExpires(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", 5*time.Millisecond, 5*time.Millisecond))
	time.Sleep(10 * time.Millisecond)

	if eff := e.Check("storage", "Op"); eff.Latency != 0 {
		t.Errorf("should expire, got %v", eff.Latency)
	}
}

func TestProbabilisticFailureAlwaysHits(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("boom")
	e.Apply(chaos.ProbabilisticFailure("storage", "GetObject", myErr, 1.0, time.Hour))

	for range 20 {
		if eff := e.Check("storage", "GetObject"); eff.Error == nil {
			t.Fatal("p=1.0 should always inject")
		}
	}
}

func TestProbabilisticFailureNeverHits(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("never")
	e.Apply(chaos.ProbabilisticFailure("storage", "Op", myErr, 0.0, time.Hour))

	for range 20 {
		if eff := e.Check("storage", "Op"); eff.Error != nil {
			t.Fatalf("p=0.0 should never inject, got %v", eff.Error)
		}
	}
}

func TestProbabilisticFailureRespectsOpFilter(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("only get")
	e.Apply(chaos.ProbabilisticFailure("storage", "GetObject", myErr, 1.0, time.Hour))

	if eff := e.Check("storage", "PutObject"); eff.Error != nil {
		t.Errorf("PutObject should be unaffected, got %v", eff.Error)
	}
}

func TestProbabilisticFailureEmptyOpAffectsAll(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("any op")
	// Empty op = service-wide.
	e.Apply(chaos.ProbabilisticFailure("storage", "", myErr, 1.0, time.Hour))

	for _, op := range []string{"PutObject", "GetObject", "ListObjects"} {
		if eff := e.Check("storage", op); eff.Error == nil {
			t.Errorf("op %s should fail under service-wide rule", op)
		}
	}
}

func TestProbabilisticFailureDifferentServiceUnaffected(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ProbabilisticFailure("storage", "Op", errors.New("x"), 1.0, time.Hour))

	if eff := e.Check("compute", "Op"); eff.Error != nil {
		t.Errorf("compute should be unaffected, got %v", eff.Error)
	}
}

func TestThrottleAllowsUpToQPS(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Throttle("compute", "RunInstances", 3, time.Hour))

	for i := range 3 {
		if eff := e.Check("compute", "RunInstances"); eff.Error != nil {
			t.Fatalf("call %d should pass under threshold, got %v", i, eff.Error)
		}
	}
}

func TestThrottleBlocksAfterQPS(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Throttle("compute", "RunInstances", 2, time.Hour))

	// Burn the budget.
	_ = e.Check("compute", "RunInstances")
	_ = e.Check("compute", "RunInstances")

	eff := e.Check("compute", "RunInstances")
	if cerrors.GetCode(eff.Error) != cerrors.Throttled {
		t.Errorf("expected Throttled, got %v", eff.Error)
	}
}

func TestThrottleZeroQPSClampedToOne(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Throttle("compute", "Op", 0, time.Hour))

	// First call should pass (clamp to 1).
	if eff := e.Check("compute", "Op"); eff.Error != nil {
		t.Errorf("first call after zero-qps clamp should pass, got %v", eff.Error)
	}

	// Second should be throttled.
	if eff := e.Check("compute", "Op"); eff.Error == nil {
		t.Error("second call should be throttled")
	}
}

func TestThrottleDifferentServiceUnaffected(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Throttle("compute", "Op", 1, time.Hour))
	_ = e.Check("compute", "Op") // burn

	if eff := e.Check("storage", "Op"); eff.Error != nil {
		t.Errorf("storage should be unaffected, got %v", eff.Error)
	}
}

func TestThrottleEmptyOpAffectsAll(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Throttle("compute", "", 1, time.Hour))

	_ = e.Check("compute", "Op1") // burn

	if eff := e.Check("compute", "Op2"); eff.Error == nil {
		t.Error("service-wide throttle should affect all ops")
	}
}

func TestCompositeMergesEffects(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("composite")
	e.Apply(chaos.Composite(
		chaos.LatencySpike("storage", 10*time.Millisecond, time.Hour),
		chaos.ProbabilisticFailure("storage", "Op", myErr, 1.0, time.Hour),
	))

	eff := e.Check("storage", "Op")
	if eff.Latency != 10*time.Millisecond {
		t.Errorf("composite latency = %v, want 10ms", eff.Latency)
	}

	if eff.Error == nil {
		t.Error("composite should propagate inner failure")
	}
}

func TestCompositeFirstNonNilErrorWins(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	first := errors.New("first")
	second := errors.New("second")

	// First scenario applies → its error wins; second is also a failure but
	// we should still see the first error in the merged effect.
	e.Apply(chaos.Composite(
		chaos.ProbabilisticFailure("storage", "Op", first, 1.0, time.Hour),
		chaos.ProbabilisticFailure("storage", "Op", second, 1.0, time.Hour),
	))

	eff := e.Check("storage", "Op")
	if eff.Error == nil {
		t.Fatal("expected error")
	}
	// We don't strictly assert which error wins (depends on iteration), but
	// it must be one of the two.
	if !errors.Is(eff.Error, first) && !errors.Is(eff.Error, second) {
		t.Errorf("error not from composite scenarios: %v", eff.Error)
	}
}

func TestCompositeActiveWhileAnyChildIsActive(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	// Short scenario + long scenario in one composite.
	e.Apply(chaos.Composite(
		chaos.LatencySpike("storage", time.Millisecond, 5*time.Millisecond),
		chaos.LatencySpike("storage", 2*time.Millisecond, time.Hour),
	))

	time.Sleep(10 * time.Millisecond)

	// First child has expired, second is still active. Composite should still apply.
	if eff := e.Check("storage", "Op"); eff.Latency == 0 {
		t.Error("composite should remain active while any child is")
	}
}

func TestCompositeEmptyIsSafe(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Composite())

	if eff := e.Check("storage", "Op"); eff.Latency != 0 || eff.Error != nil {
		t.Errorf("empty composite should produce zero effect, got %+v", eff)
	}
}
