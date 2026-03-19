// Package compute provides a portable compute API with cross-cutting concerns.
package compute

import "github.com/stackshy/cloudemu/statemachine"

// VM states.
const (
	StatePending      = "pending"
	StateRunning      = "running"
	StateStopping     = "stopping"
	StateStopped      = "stopped"
	StateShuttingDown = "shutting-down"
	StateTerminated   = "terminated"
	StateRestarting   = "restarting"
)

// VMTransitions defines the legal VM state transitions.
var VMTransitions = []statemachine.Transition{
	{From: StatePending, To: StateRunning},
	{From: StateRunning, To: StateStopping},
	{From: StateRunning, To: StateShuttingDown},
	{From: StateRunning, To: StateRestarting},
	{From: StateStopping, To: StateStopped},
	{From: StateStopped, To: StatePending},
	{From: StateStopped, To: StateShuttingDown},
	{From: StateShuttingDown, To: StateTerminated},
	{From: StateRestarting, To: StateRunning},
}
