// Package statemachine provides a generic finite state machine with callbacks.
package statemachine

// Transition defines a legal state transition.
type Transition struct {
	From string
	To   string
}

// TransitionMap holds legal transitions indexed by "from" state.
type TransitionMap map[string][]string

// NewTransitionMap creates a TransitionMap from a list of transitions.
func NewTransitionMap(transitions []Transition) TransitionMap {
	tm := make(TransitionMap)
	for _, t := range transitions {
		tm[t.From] = append(tm[t.From], t.To)
	}
	return tm
}

// IsAllowed checks if a transition from one state to another is legal.
func (tm TransitionMap) IsAllowed(from, to string) bool {
	allowed, ok := tm[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}
