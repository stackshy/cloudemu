package bedrockagentruntime

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Invocation constraints from the service model and the user guide's quotas.
const (
	maxInvocationDesc = 200
	maxSessionSteps   = 1000
)

// CreateInvocation adds an invocation to an active session. A caller-chosen id
// must be a UUID and unique in the session.
func (m *Mock) CreateInvocation(_ context.Context, sessionID, invocationID, description string) (*driver.Invocation, error) {
	var v violations

	v.pattern("invocationId", invocationID, uuidRE, uuidPattern)
	v.maxLen("description", description, maxInvocationDesc)

	if err := v.err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.activeSession(sessionID)
	if err != nil {
		return nil, err
	}

	if invocationID == "" {
		if invocationID, err = newUUID(); err != nil {
			return nil, err
		}
	}

	inv := &driver.Invocation{ID: invocationID, SessionID: s.ID, Description: description, CreatedAt: m.nowString()}
	if !m.invocations.SetIfAbsent(s.ID+"/"+invocationID, inv) {
		return nil, errors.Newf(errors.AlreadyExists, "invocation %s already exists in session %s", invocationID, s.ID)
	}

	m.touchSession(s)

	result := *inv

	return &result, nil
}

// ListInvocations lists one page of a session's invocations, newest first.
func (m *Mock) ListInvocations(_ context.Context, sessionID string, page driver.Page) ([]driver.Invocation, string, error) {
	s, err := m.findSession(sessionID)
	if err != nil {
		return nil, "", err
	}

	var out []driver.Invocation

	for key, inv := range m.invocations.All() {
		if strings.HasPrefix(key, s.ID+"/") {
			out = append(out, *inv)
		}
	}

	sortByTimeDesc(out, func(i driver.Invocation) string { return i.CreatedAt + i.ID })

	return paginate(out, page, defaultListPage)
}

// PutInvocationStep records a step of an invocation in an active session. A
// session holds at most 1000 steps.
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) PutInvocationStep(_ context.Context, in driver.InvocationStepInput) (string, error) {
	if err := validateStep(&in); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.activeSession(in.SessionID)
	if err != nil {
		return "", err
	}

	if !m.invocations.Has(s.ID + "/" + in.InvocationID) {
		return "", errors.Newf(errors.NotFound, "invocation %q not found in session %s", in.InvocationID, s.ID)
	}

	if countPrefix(m.steps, s.ID+"/") >= maxSessionSteps {
		return "", errors.Newf(errors.ResourceExhausted, "session %s already has %d invocation steps", s.ID, maxSessionSteps)
	}

	stepID := in.StepID
	if stepID == "" {
		if stepID, err = newUUID(); err != nil {
			return "", err
		}
	}

	m.steps.Set(s.ID+"/"+in.InvocationID+"/"+stepID, &driver.InvocationStep{
		ID: stepID, SessionID: s.ID, InvocationID: in.InvocationID, StepTime: in.StepTime, Payload: copyRaw(in.Payload),
	})
	m.touchSession(s)

	return stepID, nil
}

// GetInvocationStep returns a recorded step.
func (m *Mock) GetInvocationStep(_ context.Context, sessionID, invocationID, stepID string) (*driver.InvocationStep, error) {
	s, err := m.findSession(sessionID)
	if err != nil {
		return nil, err
	}

	step, ok := m.steps.Get(s.ID + "/" + invocationID + "/" + stepID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "invocation step %q not found", stepID)
	}

	result := *step
	result.Payload = copyRaw(step.Payload)

	return &result, nil
}

// ListInvocationSteps lists one page of a session's steps, of one invocation
// when invocationID is set, newest first.
func (m *Mock) ListInvocationSteps(_ context.Context, sessionID, invocationID string, page driver.Page) (
	[]driver.InvocationStep, string, error,
) {
	s, err := m.findSession(sessionID)
	if err != nil {
		return nil, "", err
	}

	prefix := s.ID + "/"
	if invocationID != "" {
		prefix += invocationID + "/"
	}

	var out []driver.InvocationStep

	for key, step := range m.steps.All() {
		if strings.HasPrefix(key, prefix) {
			c := *step
			c.Payload = nil
			out = append(out, c)
		}
	}

	sortByTimeDesc(out, func(st driver.InvocationStep) string { return st.StepTime + st.ID })

	return paginate(out, page, defaultListPage)
}

func validateStep(in *driver.InvocationStepInput) error {
	var v violations

	v.required("invocationIdentifier", in.InvocationID == "")
	v.pattern("invocationIdentifier", in.InvocationID, uuidRE, uuidPattern)
	v.pattern("invocationStepId", in.StepID, uuidRE, uuidPattern)
	v.required("invocationStepTime", in.StepTime == "")
	v.required("payload", isNull(in.Payload))

	return v.err()
}

func countPrefix[V any](store *memstore.Store[V], prefix string) int {
	n := 0

	for key := range store.All() {
		if strings.HasPrefix(key, prefix) {
			n++
		}
	}

	return n
}

func deleteByPrefix[V any](store *memstore.Store[V], prefix string) {
	for key := range store.All() {
		if strings.HasPrefix(key, prefix) {
			store.Delete(key)
		}
	}
}
