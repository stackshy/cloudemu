package statemachine

import (
	"fmt"
	"sync"

	cerrors "github.com/stackshy/cloudemu/errors"
)

// Callback is called when a state transition occurs.
type Callback func(resourceID, from, to string)

// Machine is a generic finite state machine.
type Machine struct {
	mu          sync.RWMutex
	states      map[string]string // resourceID -> state
	transitions TransitionMap
	callbacks   []Callback
}

// New creates a new Machine with the given legal transitions.
func New(transitions []Transition) *Machine {
	return &Machine{
		states:      make(map[string]string),
		transitions: NewTransitionMap(transitions),
	}
}

// OnTransition registers a callback for state transitions.
func (m *Machine) OnTransition(cb Callback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// SetState sets the initial state for a resource.
func (m *Machine) SetState(resourceID, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[resourceID] = state
}

// GetState returns the current state of a resource.
func (m *Machine) GetState(resourceID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[resourceID]
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "resource %s not found", resourceID)
	}
	return state, nil
}

// Transition attempts to transition a resource to a new state.
func (m *Machine) Transition(resourceID, to string) error {
	m.mu.Lock()
	from, ok := m.states[resourceID]
	if !ok {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "resource %s not found", resourceID)
	}
	if !m.transitions.IsAllowed(from, to) {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.FailedPrecondition,
			"transition from %s to %s not allowed for resource %s", from, to, resourceID)
	}
	m.states[resourceID] = to
	callbacks := make([]Callback, len(m.callbacks))
	copy(callbacks, m.callbacks)
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb(resourceID, from, to)
	}
	return nil
}

// Remove removes a resource from the state machine.
func (m *Machine) Remove(resourceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, resourceID)
}

// Resources returns all resource IDs and their states.
func (m *Machine) Resources() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]string, len(m.states))
	for k, v := range m.states {
		result[k] = v
	}
	return result
}

// String returns the state of a specific resource for debugging.
func (m *Machine) String(resourceID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[resourceID]
	if !ok {
		return fmt.Sprintf("%s: <not found>", resourceID)
	}
	return fmt.Sprintf("%s: %s", resourceID, state)
}
