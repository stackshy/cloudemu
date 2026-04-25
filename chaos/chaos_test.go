package chaos_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
)

func TestNewWithNilClockUsesRealClock(t *testing.T) {
	e := chaos.New(nil)
	defer e.Stop()

	// Apply a scenario; if the clock plumbing is broken, Active won't fire.
	e.Apply(chaos.LatencySpike("storage", time.Millisecond, time.Hour))

	if eff := e.Check("storage", "Op"); eff.Latency != time.Millisecond {
		t.Fatalf("expected scenario to be live, got %+v", eff)
	}
}

func TestEngineNilSafe(t *testing.T) {
	var e *chaos.Engine

	if got := e.Check("storage", "PutObject"); got.Error != nil || got.Latency != 0 {
		t.Fatalf("nil engine should be a no-op, got %+v", got)
	}
}

func TestEngineNoActiveScenariosReturnsZeroEffect(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	if eff := e.Check("storage", "Op"); eff.Latency != 0 || eff.Error != nil {
		t.Fatalf("empty engine should produce zero effect, got %+v", eff)
	}
}

func TestEngineMergesMultipleScenarios(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", 10*time.Millisecond, time.Hour))
	e.Apply(chaos.LatencySpike("storage", 20*time.Millisecond, time.Hour))

	eff := e.Check("storage", "Op")
	if eff.Latency != 30*time.Millisecond {
		t.Errorf("latencies should sum: got %v want 30ms", eff.Latency)
	}
}

func TestEngineExpiresScenariosLazily(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", time.Millisecond, 5*time.Millisecond))

	if eff := e.Check("storage", "Op"); eff.Latency == 0 {
		t.Fatal("scenario should be active immediately")
	}

	time.Sleep(10 * time.Millisecond)

	// The next Check should drop the expired scenario.
	if eff := e.Check("storage", "Op"); eff.Latency != 0 {
		t.Errorf("expected scenario to expire, got %+v", eff)
	}
}

func TestActiveStopRemovesScenario(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	a := e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if e.Check("storage", "Op").Error == nil {
		t.Fatal("scenario should be active")
	}

	a.Stop()

	if eff := e.Check("storage", "Op"); eff.Error != nil {
		t.Errorf("scenario should be cleared after Stop, got %v", eff.Error)
	}
}

func TestActiveStopOnNilSafe(t *testing.T) {
	var a *chaos.Active

	a.Stop() // should not panic
}

func TestActiveStopIdempotent(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	a := e.Apply(chaos.ServiceOutage("storage", time.Hour))

	a.Stop()
	a.Stop() // second call should be a no-op
}

func TestEngineStopClearsAllScenarios(t *testing.T) {
	e := chaos.New(config.RealClock{})

	e.Apply(chaos.ServiceOutage("storage", time.Hour))
	e.Apply(chaos.LatencySpike("compute", time.Second, time.Hour))

	e.Stop()

	if eff := e.Check("storage", "Op"); eff.Error != nil {
		t.Errorf("Stop should clear storage outage, got %v", eff.Error)
	}

	if eff := e.Check("compute", "Op"); eff.Latency != 0 {
		t.Errorf("Stop should clear compute latency, got %v", eff.Latency)
	}
}

func TestRecordedCapturesAppliedEffects(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	for range 3 {
		_ = e.Check("storage", "Op")
	}

	rec := e.Recorded()
	if len(rec) != 3 {
		t.Fatalf("len=%d want 3", len(rec))
	}

	for _, r := range rec {
		if r.Service != "storage" || r.Operation != "Op" {
			t.Errorf("recorded event wrong: %+v", r)
		}

		if r.Effect.Error == nil {
			t.Errorf("recorded event missing effect error: %+v", r)
		}

		if r.When.IsZero() {
			t.Errorf("recorded event has zero timestamp: %+v", r)
		}
	}
}

func TestRecordedSkipsNoOps(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	// No scenarios applied — the call should produce no recording.
	_ = e.Check("storage", "Op")

	if rec := e.Recorded(); len(rec) != 0 {
		t.Errorf("expected no recordings without active scenario, got %d", len(rec))
	}
}

func TestResetClearsRecorded(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", time.Hour))
	_ = e.Check("storage", "Op")

	e.Reset()

	if rec := e.Recorded(); len(rec) != 0 {
		t.Errorf("Reset should clear recorded events, got %d", len(rec))
	}
}

func TestEngineConcurrentSafe(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", time.Microsecond, time.Hour))

	const workers = 50

	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 100 {
				_ = e.Check("storage", "Op")
			}
		}()
	}

	wg.Wait()

	if rec := e.Recorded(); len(rec) != workers*100 {
		t.Errorf("recorded=%d want %d", len(rec), workers*100)
	}
}

func TestErrChaosInjectedExported(t *testing.T) {
	if chaos.ErrChaosInjected == nil {
		t.Fatal("ErrChaosInjected should be a non-nil sentinel")
	}

	if chaos.ErrChaosInjected.Error() == "" {
		t.Error("ErrChaosInjected should have a message")
	}
}
