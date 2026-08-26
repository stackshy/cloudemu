package chaos

import (
	"sync/atomic"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// window holds the common "active from start to start+duration" timing logic.
// All scenarios with a duration embed it. The start (and thus end) is bound to
// the engine's clock when the scenario is applied — see bind — so the window is
// relative to the engine clock, not wall-clock at construction. This keeps
// FakeClock-driven deterministic tests working. Until bound, the window is
// inactive.
type window struct {
	duration time.Duration
	start    time.Time
	end      time.Time
	bound    bool
}

func newWindow(duration time.Duration) window {
	return window{duration: duration}
}

// bind stamps the window's start/end from now (the engine clock at Apply time).
// It is idempotent: an already-bound window keeps its original start, so a
// scenario is only ever anchored once.
func (w *window) bind(now time.Time) {
	if w.bound {
		return
	}

	w.start = now
	w.end = now.Add(w.duration)
	w.bound = true
}

func (w window) active(now time.Time) bool {
	if !w.bound {
		return false
	}

	return !now.Before(w.start) && now.Before(w.end)
}

// outage makes every operation on a service fail with ServiceUnavailable.
type outage struct {
	window
	service string
}

// ServiceOutage simulates service svc being completely down for duration.
// Every call to it returns a ServiceUnavailable error during the window;
// after the window, calls succeed normally again.
func ServiceOutage(svc string, duration time.Duration) Scenario {
	return &outage{
		window:  newWindow(duration),
		service: svc,
	}
}

func (o *outage) EffectOn(_ time.Time, service, _ string) Effect {
	if service != o.service {
		return Effect{}
	}

	return Effect{Error: cerrors.New(cerrors.Unavailable, o.service+": service unavailable (chaos)")}
}

func (o *outage) Active(now time.Time) bool { return o.window.active(now) }

// latencySpike adds extra latency to every op on a service.
type latencySpike struct {
	window
	service string
	extra   time.Duration
}

// LatencySpike makes every call to service svc take an extra extra duration
// for the next duration window. Useful for testing client-side timeouts.
func LatencySpike(svc string, extra, duration time.Duration) Scenario {
	return &latencySpike{
		window:  newWindow(duration),
		service: svc,
		extra:   extra,
	}
}

func (l *latencySpike) EffectOn(_ time.Time, service, _ string) Effect {
	if service != l.service {
		return Effect{}
	}

	return Effect{Latency: l.extra}
}

func (l *latencySpike) Active(now time.Time) bool { return l.window.active(now) }

// probabilisticFailure injects err with probability p for the configured op.
type probabilisticFailure struct {
	window
	service string
	op      string
	err     error
	p       float64
}

// ProbabilisticFailure injects err on a fraction p (0.0–1.0) of calls to
// service.operation, for the next duration window. If op is empty, applies
// to every operation on the service.
func ProbabilisticFailure(svc, op string, err error, p float64, duration time.Duration) Scenario {
	return &probabilisticFailure{
		window:  newWindow(duration),
		service: svc,
		op:      op,
		err:     err,
		p:       p,
	}
}

func (f *probabilisticFailure) EffectOn(_ time.Time, service, op string) Effect {
	if service != f.service {
		return Effect{}
	}

	if f.op != "" && op != f.op {
		return Effect{}
	}

	if randFloat() >= f.p {
		return Effect{}
	}

	return Effect{Error: f.err}
}

func (f *probabilisticFailure) Active(now time.Time) bool { return f.window.active(now) }

// throttle returns Throttled after a token-bucket-like rate is exceeded.
// Uses a 1-second sliding count which is good enough for testing harnesses.
type throttle struct {
	window
	service string
	op      string
	qps     int

	// state
	startSec int64 // unix seconds of the current bucket
	count    int64
}

// Throttle simulates rate-limit pressure on service.operation: once qps calls
// are seen in any given 1-second wall-clock bucket, further calls in that
// bucket return Throttled. Resets each second. Active for duration.
func Throttle(svc, op string, qps int, duration time.Duration) Scenario {
	if qps < 1 {
		qps = 1
	}

	return &throttle{
		window:  newWindow(duration),
		service: svc,
		op:      op,
		qps:     qps,
	}
}

func (t *throttle) EffectOn(now time.Time, service, op string) Effect {
	if service != t.service {
		return Effect{}
	}

	if t.op != "" && op != t.op {
		return Effect{}
	}

	sec := now.Unix()

	// Reset bucket if we crossed a second boundary.
	if atomic.LoadInt64(&t.startSec) != sec {
		atomic.StoreInt64(&t.startSec, sec)
		atomic.StoreInt64(&t.count, 0)
	}

	c := atomic.AddInt64(&t.count, 1)
	if c <= int64(t.qps) {
		return Effect{}
	}

	return Effect{Error: cerrors.New(cerrors.Throttled, t.service+": throttled (chaos)")}
}

func (t *throttle) Active(now time.Time) bool { return t.window.active(now) }

// composite combines multiple scenarios. Latencies sum; the first non-nil
// error wins; the composite is active while any inner scenario is active.
type composite struct {
	scenarios []Scenario
}

// Composite combines several scenarios into one. The merged Effect adds
// latencies and uses the first non-nil error.
func Composite(scenarios ...Scenario) Scenario {
	return &composite{scenarios: scenarios}
}

func (c *composite) EffectOn(now time.Time, service, operation string) Effect {
	var out Effect

	for _, s := range c.scenarios {
		eff := s.EffectOn(now, service, operation)
		out.Latency += eff.Latency

		if out.Error == nil && eff.Error != nil {
			out.Error = eff.Error
		}
	}

	return out
}

func (c *composite) Active(now time.Time) bool {
	for _, s := range c.scenarios {
		if s.Active(now) {
			return true
		}
	}

	return false
}

// bind propagates the engine clock to every child that has a time-bounded
// window, so a composite anchors all its children at Apply time.
func (c *composite) bind(now time.Time) {
	for _, s := range c.scenarios {
		if b, ok := s.(clockBinder); ok {
			b.bind(now)
		}
	}
}
