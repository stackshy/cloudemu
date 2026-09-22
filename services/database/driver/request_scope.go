package driver

import (
	"context"
	"sync"
)

// RequestScope ties the per-item driver calls one multi-item wire request fans
// out into (BatchWriteItem, TransactGetItems, …) back to that single request, so
// a provider can attribute its per-request metrics to the request's own
// operation name — once per table — instead of to each underlying item call.
//
// The wire layer opens a scope with WithRequestScope and calls the returned
// finish func once the request has succeeded; a provider registers, per table,
// the work to run at finish (for example publishing SuccessfulRequestLatency).
// A request that fails never calls finish, so nothing registered runs.
type RequestScope struct {
	operation string

	mu       sync.Mutex
	tables   map[string]struct{}
	finalize []func()
}

type requestScopeKey struct{}

// WithRequestScope returns a context carrying a new scope for operation, and the
// finish func that runs everything providers registered on it, in order.
func WithRequestScope(ctx context.Context, operation string) (scoped context.Context, finish func()) {
	s := &RequestScope{operation: operation, tables: map[string]struct{}{}}

	return context.WithValue(ctx, requestScopeKey{}, s), s.finish
}

// RequestScopeFrom returns the scope ctx carries, or nil outside a scoped request.
func RequestScopeFrom(ctx context.Context) *RequestScope {
	s, _ := ctx.Value(requestScopeKey{}).(*RequestScope)

	return s
}

// Operation is the wire operation the scope belongs to.
func (s *RequestScope) Operation() string { return s.operation }

// OnFinish registers fn to run when the request finishes successfully, at most
// once per table: a later registration for an already-registered table is
// dropped, so a request touching one table many times still yields one run.
func (s *RequestScope) OnFinish(table string, fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, seen := s.tables[table]; seen {
		return
	}

	s.tables[table] = struct{}{}
	s.finalize = append(s.finalize, fn)
}

func (s *RequestScope) finish() {
	s.mu.Lock()
	fns := s.finalize
	s.finalize = nil
	s.mu.Unlock()

	for _, fn := range fns {
		fn()
	}
}
