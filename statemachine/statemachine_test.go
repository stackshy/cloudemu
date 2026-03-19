package statemachine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vmTransitions() []Transition {
	return []Transition{
		{From: "pending", To: "running"},
		{From: "running", To: "stopped"},
		{From: "running", To: "terminated"},
		{From: "stopped", To: "running"},
		{From: "stopped", To: "terminated"},
	}
}

func TestTransitionMap_IsAllowed(t *testing.T) {
	tests := []struct {
		name   string
		from   string
		to     string
		expect bool
	}{
		{name: "valid pending to running", from: "pending", to: "running", expect: true},
		{name: "valid running to stopped", from: "running", to: "stopped", expect: true},
		{name: "valid running to terminated", from: "running", to: "terminated", expect: true},
		{name: "valid stopped to running", from: "stopped", to: "running", expect: true},
		{name: "invalid pending to terminated", from: "pending", to: "terminated", expect: false},
		{name: "invalid terminated to running", from: "terminated", to: "running", expect: false},
		{name: "unknown source state", from: "unknown", to: "running", expect: false},
	}

	tm := NewTransitionMap(vmTransitions())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, tm.IsAllowed(tc.from, tc.to))
		})
	}
}

func TestMachine_SetAndGetState(t *testing.T) {
	m := New(vmTransitions())
	m.SetState("vm-1", "pending")

	state, err := m.GetState("vm-1")
	require.NoError(t, err)
	assert.Equal(t, "pending", state)
}

func TestMachine_GetState_NotFound(t *testing.T) {
	m := New(vmTransitions())

	_, err := m.GetState("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMachine_Transition_Valid(t *testing.T) {
	tests := []struct {
		name       string
		initial    string
		target     string
		expectSt   string
	}{
		{name: "pending to running", initial: "pending", target: "running", expectSt: "running"},
		{name: "running to stopped", initial: "running", target: "stopped", expectSt: "stopped"},
		{name: "stopped to terminated", initial: "stopped", target: "terminated", expectSt: "terminated"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(vmTransitions())
			m.SetState("vm-1", tc.initial)

			err := m.Transition("vm-1", tc.target)
			require.NoError(t, err)

			state, err := m.GetState("vm-1")
			require.NoError(t, err)
			assert.Equal(t, tc.expectSt, state)
		})
	}
}

func TestMachine_Transition_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		target  string
	}{
		{name: "pending to terminated", initial: "pending", target: "terminated"},
		{name: "terminated to running", initial: "terminated", target: "running"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(vmTransitions())
			m.SetState("vm-1", tc.initial)

			err := m.Transition("vm-1", tc.target)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not allowed")
		})
	}
}

func TestMachine_Transition_ResourceNotFound(t *testing.T) {
	m := New(vmTransitions())

	err := m.Transition("ghost", "running")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMachine_OnTransition_Callback(t *testing.T) {
	m := New(vmTransitions())
	m.SetState("vm-1", "pending")

	var called bool
	var capturedID, capturedFrom, capturedTo string

	m.OnTransition(func(id, from, to string) {
		called = true
		capturedID = id
		capturedFrom = from
		capturedTo = to
	})

	err := m.Transition("vm-1", "running")
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "vm-1", capturedID)
	assert.Equal(t, "pending", capturedFrom)
	assert.Equal(t, "running", capturedTo)
}

func TestMachine_Remove(t *testing.T) {
	m := New(vmTransitions())
	m.SetState("vm-1", "running")

	m.Remove("vm-1")

	_, err := m.GetState("vm-1")
	require.Error(t, err)
}

func TestMachine_Resources(t *testing.T) {
	m := New(vmTransitions())
	m.SetState("vm-1", "running")
	m.SetState("vm-2", "stopped")

	resources := m.Resources()
	assert.Len(t, resources, 2)
	assert.Equal(t, "running", resources["vm-1"])
	assert.Equal(t, "stopped", resources["vm-2"])
}

func TestMachine_String(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		setup      bool
		state      string
		contains   string
	}{
		{name: "existing resource", resourceID: "vm-1", setup: true, state: "running", contains: "running"},
		{name: "missing resource", resourceID: "vm-2", setup: false, contains: "not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(vmTransitions())

			if tc.setup {
				m.SetState(tc.resourceID, tc.state)
			}

			result := m.String(tc.resourceID)
			assert.Contains(t, result, tc.contains)
		})
	}
}
