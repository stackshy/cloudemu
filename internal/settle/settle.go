// Package settle models AWS asynchronous resource-state transitions
// (pending->running, creating->available, PENDING_VALIDATION->ISSUED, ...) as a
// lazy, read-time overlay driven purely by a clock.
//
// A resource stores its FINAL settled state (what the logical state machine and
// internal consumers expect) plus a Window recording an intermediate state and
// the instant it settles. Describe/Get emits Window.Observe(clock.Now(), final):
// the intermediate value until readyAt, then the final value. Observe never
// mutates, so it composes with a provider's existing read lock with no
// escalation, and it uses no goroutines or timers — advancing a FakeClock (in
// tests) or wall-clock (under `cloudemu serve`) is what moves the observed
// state. The zero-value Window is inactive: Observe returns the final state
// unconditionally, which preserves the synchronous behavior callers had before
// any settling was configured.
package settle

import "time"

// Default settle durations per resource type. They are deliberately small so a
// real SDK waiter (whose minimum poll delay is 15-30s) succeeds on its first
// poll, while an immediate Describe still observes the intermediate state.
const (
	DefaultInstanceSettle    = 2 * time.Second // EC2 instance pending->running
	DefaultVolumeSettle      = 1 * time.Second // EBS volume creating->available
	DefaultDBInstanceSettle  = 3 * time.Second // RDS instance creating->available
	DefaultDBSnapshotSettle  = 2 * time.Second // RDS snapshot creating->available
	DefaultDBRebootSettle    = 1 * time.Second // RDS available->rebooting->available
	DefaultCertificateSettle = 2 * time.Second // ACM PENDING_VALIDATION->ISSUED
	DefaultExecutionSettle   = 1 * time.Second // SFN RUNNING->SUCCEEDED
)

// Window is a read-time overlay describing a resource still settling into its
// final state. The zero value is inactive.
type Window struct {
	// Intermediate is the state observed before the window elapses (e.g.
	// "pending", "creating", "PENDING_VALIDATION", "RUNNING", "rebooting").
	Intermediate string
	// ReadyAt is when the final state becomes observable.
	ReadyAt time.Time
	active  bool
}

// Pending builds a window that shows intermediate until createdAt+d, then the
// final state. A non-positive d yields an inactive (zero-equivalent) window, so
// a single call site cleanly expresses "async disabled".
func Pending(intermediate string, createdAt time.Time, d time.Duration) Window {
	if d <= 0 {
		return Window{}
	}

	return Window{Intermediate: intermediate, ReadyAt: createdAt.Add(d), active: true}
}

// Observe returns the state to report at now: the intermediate value while the
// window is active and unelapsed, otherwise final.
func (w Window) Observe(now time.Time, final string) string {
	if w.active && now.Before(w.ReadyAt) {
		return w.Intermediate
	}

	return final
}

// Settled reports whether the window has elapsed (or was never active). Useful
// for progress fields such as RDS snapshot PercentProgress (0 while unsettled,
// 100 once settled).
func (w Window) Settled(now time.Time) bool {
	return !(w.active && now.Before(w.ReadyAt))
}
