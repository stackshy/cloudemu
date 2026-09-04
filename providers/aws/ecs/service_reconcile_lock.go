package ecs

import "sync"

// serviceReconcileLock hands out one mutex per service key (cluster+name, see
// serviceKey), created on first use. It serializes reconcileServiceAfterStop's
// whole read-live-counts -> decide-shortfall -> launch-replacements -> commit
// sequence for a single service, closing the TOCTOU that otherwise lets two
// concurrent StopTask calls on two different tasks of the same service each
// read the pre-replacement counts, each independently compute the full
// shortfall, and each launch a replacement — over-provisioning the service
// above desiredCount with nothing to ever scale it back down. Keying by
// service (rather than one global lock) keeps unrelated services reconciling
// concurrently.
type serviceReconcileLock struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newServiceReconcileLock() *serviceReconcileLock {
	return &serviceReconcileLock{locks: make(map[string]*sync.Mutex)}
}

// lock acquires the mutex for key and returns its unlock func. The returned
// func must be called (typically via defer) to release the lock.
func (l *serviceReconcileLock) lock(key string) func() {
	l.mu.Lock()

	m, ok := l.locks[key]
	if !ok {
		m = &sync.Mutex{}
		l.locks[key] = m
	}

	l.mu.Unlock()

	m.Lock()

	return m.Unlock
}
