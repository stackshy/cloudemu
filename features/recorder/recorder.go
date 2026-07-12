package recorder

import (
	"sync"
	"time"
)

// Recorder records API calls for later assertion.
type Recorder struct {
	mu    sync.RWMutex
	calls []Call
}

// New creates a new Recorder.
func New() *Recorder {
	return &Recorder{}
}

// Record records a call.
func (r *Recorder) Record(service, operation string, input, output any, err error, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, Call{
		Service:   service,
		Operation: operation,
		Input:     input,
		Output:    output,
		Error:     err,
		Timestamp: time.Now(),
		Duration:  duration,
	})
}

// Calls returns a copy of all recorded calls.
func (r *Recorder) Calls() []Call {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Call, len(r.calls))
	copy(result, r.calls)

	return result
}

// CallCount returns the total number of recorded calls.
func (r *Recorder) CallCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.calls)
}

// CallsFor returns all calls matching the given service and operation.
func (r *Recorder) CallsFor(service, operation string) []Call {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Call

	for _, c := range r.calls {
		if c.Service == service && c.Operation == operation {
			result = append(result, c)
		}
	}

	return result
}

// CallCountFor returns the number of calls matching the given service and operation.
func (r *Recorder) CallCountFor(service, operation string) int {
	return len(r.CallsFor(service, operation))
}

// Reset clears all recorded calls.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = nil
}

// LastCall returns the most recent call, or nil if none.
func (r *Recorder) LastCall() *Call {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.calls) == 0 {
		return nil
	}

	c := r.calls[len(r.calls)-1]

	return &c
}
