package recorder

import (
	"fmt"
	"testing"
)

// Matcher provides fluent test assertions for recorded calls.
type Matcher struct {
	t     *testing.T
	calls []Call
}

// NewMatcher creates a Matcher from a recorder.
func NewMatcher(t *testing.T, r *Recorder) *Matcher {
	t.Helper()
	return &Matcher{t: t, calls: r.Calls()}
}

// ForService filters calls by service.
func (m *Matcher) ForService(service string) *Matcher {
	m.t.Helper()
	var filtered []Call
	for _, c := range m.calls {
		if c.Service == service {
			filtered = append(filtered, c)
		}
	}
	return &Matcher{t: m.t, calls: filtered}
}

// ForOperation filters calls by operation.
func (m *Matcher) ForOperation(op string) *Matcher {
	m.t.Helper()
	var filtered []Call
	for _, c := range m.calls {
		if c.Operation == op {
			filtered = append(filtered, c)
		}
	}
	return &Matcher{t: m.t, calls: filtered}
}

// Count asserts the number of matching calls.
func (m *Matcher) Count(expected int) *Matcher {
	m.t.Helper()
	if len(m.calls) != expected {
		m.t.Errorf("expected %d calls, got %d", expected, len(m.calls))
	}
	return m
}

// HasCalls asserts that there is at least one matching call.
func (m *Matcher) HasCalls() *Matcher {
	m.t.Helper()
	if len(m.calls) == 0 {
		m.t.Error("expected at least one call, got none")
	}
	return m
}

// NoCalls asserts that there are no matching calls.
func (m *Matcher) NoCalls() *Matcher {
	m.t.Helper()
	if len(m.calls) != 0 {
		m.t.Errorf("expected no calls, got %d", len(m.calls))
	}
	return m
}

// NoErrors asserts that no calls had errors.
func (m *Matcher) NoErrors() *Matcher {
	m.t.Helper()
	for i, c := range m.calls {
		if c.Error != nil {
			m.t.Errorf("call %d had error: %v", i, c.Error)
		}
	}
	return m
}

// AllErrored asserts that all calls had errors.
func (m *Matcher) AllErrored() *Matcher {
	m.t.Helper()
	for i, c := range m.calls {
		if c.Error == nil {
			m.t.Errorf("expected call %d to have error, but it succeeded", i)
		}
	}
	return m
}

// String returns a summary of matched calls.
func (m *Matcher) String() string {
	return fmt.Sprintf("Matcher{calls: %d}", len(m.calls))
}
