package lambda

import (
	"context"
	"sync"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// maxPinAttempts bounds how often pinDeployment re-reads a function whose
// deployment a concurrent code update retired. One retry normally suffices: a
// fresh copy only serves deployments no update has retired yet.
const maxPinAttempts = 8

// deployPins counts the invocations running on each engine deployment, keyed by
// engine key, so a code update can wait for them before it removes a
// deployment it retired.
type deployPins struct {
	mu    sync.Mutex
	idle  *sync.Cond
	count map[string]int
}

func newDeployPins() *deployPins {
	p := &deployPins{count: map[string]int{}}
	p.idle = sync.NewCond(&p.mu)

	return p
}

// pin records an invocation starting on key.
func (p *deployPins) pin(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.count[key]++
}

// unpin records an invocation on key finishing.
func (p *deployPins) unpin(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.count[key]--
	if p.count[key] <= 0 {
		delete(p.count, key)
		p.idle.Broadcast()
	}
}

// wait blocks until no invocation is running on key.
func (p *deployPins) wait(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for p.count[key] > 0 {
		p.idle.Wait()
	}
}

// servesEngineKey reports whether an invocation of fd can still run under key:
// it is the live deployment or the prior revision's. A retired key is neither.
func (fd *funcData) servesEngineKey(key string) bool {
	if key == fd.engineKey() {
		return true
	}

	return fd.prior != nil && key == fd.prior.engineKey(fd.info.Name)
}

// pinDeployment pins the engine deployment the invocation fd describes runs
// under and returns its key; the caller unpins it when the invocation ends.
//
// fd is an unlocked copy, so a code update may have retired its deployment
// since it was taken. The function is re-read after pinning: if the stored
// function no longer serves the key, fd is refreshed (keeping executedVersion)
// and the pin is retried. A code update removes a retired deployment only after
// it stored the function without it and pins.wait returned, so a pin that
// passes this check holds a deployment that stays until the invocation ends.
func (m *Mock) pinDeployment(ctx context.Context, fd *funcData, executedVersion string) (string, error) {
	name := fd.info.Name

	for range maxPinAttempts {
		key := fd.engineKey()
		m.pins.pin(key)

		cur, ok := m.funcs.Get(name)
		if !ok {
			m.pins.unpin(key)

			return "", m.errFunctionNotFound(ctx, name, "")
		}

		if cur.servesEngineKey(key) {
			return key, nil
		}

		m.pins.unpin(key)

		*fd = cur
		m.servePrior(fd, executedVersion)
	}

	return "", cerrors.Newf(cerrors.Internal, "invoke function %s: deployment changed during the invocation", name)
}
