// Package chaos lets tests deliberately fail or slow down CloudEmu services
// in controlled, time-bounded ways — so app code that handles cloud failure
// can be exercised without waiting for real cloud to misbehave.
//
// Typical usage:
//
//	engine := chaos.New(config.RealClock{})
//	defer engine.Stop()
//
//	// Wrap a driver before handing it to the portable API or HTTP server
//	chaosS3 := chaos.WrapBucket(aws.S3, engine)
//	srv := awsserver.New(awsserver.Drivers{S3: chaosS3})
//
//	// Inject a failure scenario
//	engine.Apply(chaos.ServiceOutage("storage", 5*time.Second))
//
//	// SDK calls now fail for 5s, then recover automatically
package chaos

import (
	"errors"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
)

// Effect is what a chaos scenario produces for a given call. Either field may
// be the zero value (no latency / no error). When both are zero, the call
// proceeds normally.
type Effect struct {
	Latency time.Duration
	Error   error
}

// Scenario describes a chaos pattern. It's queried by the engine on every
// driver call to decide what (if anything) should happen to it.
type Scenario interface {
	// EffectOn returns the effect to apply to a given service+operation call
	// at this moment, or a zero Effect if the scenario doesn't apply.
	EffectOn(now time.Time, service, operation string) Effect

	// Active returns true while the scenario is in its effective window.
	// Once Active returns false, the engine drops it.
	Active(now time.Time) bool
}

// Active is a handle returned by Apply that lets a caller cancel a scenario
// before its natural expiry.
type Active struct {
	engine *Engine
	id     uint64
}

// Stop removes the scenario from the engine. Safe to call multiple times.
func (a *Active) Stop() {
	if a == nil || a.engine == nil {
		return
	}

	a.engine.remove(a.id)
}

// Engine is the registry and dispatcher of active scenarios. It's safe for
// concurrent use.
type Engine struct {
	clock config.Clock

	mu       sync.RWMutex
	next     uint64
	active   map[uint64]Scenario
	recorded []Recorded
}

// Recorded captures one chaos event for post-test inspection.
type Recorded struct {
	When      time.Time
	Service   string
	Operation string
	Effect    Effect
}

// New returns an engine that uses clock for time-based scenario activation.
// Pass a config.FakeClock for deterministic tests.
func New(clock config.Clock) *Engine {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &Engine{
		clock:  clock,
		active: make(map[uint64]Scenario),
	}
}

// Apply registers a scenario. The returned Active can be used to stop it
// before it expires.
func (e *Engine) Apply(s Scenario) *Active {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.next++
	id := e.next
	e.active[id] = s

	return &Active{engine: e, id: id}
}

func (e *Engine) remove(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.active, id)
}

// Stop removes every active scenario. Idiomatic to defer at test setup.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.active = make(map[uint64]Scenario)
}

// Check is what driver wrappers call on every operation. It returns the
// merged effect from every active scenario:
//   - latencies sum (worst case)
//   - the first non-nil error wins
//
// Expired scenarios are dropped lazily here.
func (e *Engine) Check(service, operation string) Effect {
	if e == nil {
		return Effect{}
	}

	now := e.clock.Now()

	e.mu.Lock()
	defer e.mu.Unlock()

	var merged Effect

	for id, s := range e.active {
		if !s.Active(now) {
			delete(e.active, id)

			continue
		}

		eff := s.EffectOn(now, service, operation)
		merged.Latency += eff.Latency

		if merged.Error == nil && eff.Error != nil {
			merged.Error = eff.Error
		}
	}

	if merged.Latency > 0 || merged.Error != nil {
		e.recorded = append(e.recorded, Recorded{
			When: now, Service: service, Operation: operation, Effect: merged,
		})
	}

	return merged
}

// Recorded returns a snapshot of every chaos event the engine has seen since
// the last Reset. Useful for post-test assertions.
func (e *Engine) Recorded() []Recorded {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]Recorded, len(e.recorded))
	copy(out, e.recorded)

	return out
}

// Reset clears the recorded events buffer. Active scenarios are untouched.
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.recorded = nil
}

// ErrChaosInjected is the sentinel-style error type the engine wraps when a
// scenario provides no specific error. Callers can errors.Is against it.
var ErrChaosInjected = errors.New("chaos: injected failure")
