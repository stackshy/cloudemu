package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// triggerData is a trigger plus its own lock.
type triggerData struct {
	trigger driver.Trigger
	mu      sync.RWMutex
}

// CreateTrigger creates a workflow trigger, atomically, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateTrigger(_ context.Context, t driver.Trigger) (string, error) {
	if !validName(t.Name) {
		return "", invalidInput("trigger name %q is invalid", t.Name)
	}

	switch t.Type {
	case "SCHEDULED":
		if t.Schedule == "" {
			return "", invalidInput("a SCHEDULED trigger requires a Schedule")
		}
	case "CONDITIONAL":
		if len(t.Predicate) == 0 {
			return "", invalidInput("a CONDITIONAL trigger requires a Predicate")
		}
	case "ON_DEMAND", "EVENT":
	default:
		return "", invalidInput("trigger type %q is invalid", t.Type)
	}

	t.State = driver.TriggerCreated
	t.CreationTime = m.now()
	stored := copyTrigger(t)

	if !m.triggers.SetIfAbsent(t.Name, &triggerData{trigger: stored}) {
		return "", alreadyExists("Trigger already exists: %s", t.Name)
	}

	return t.Name, nil
}

func (m *Mock) getTriggerData(name string) (*triggerData, error) {
	if !validName(name) {
		return nil, invalidInput("trigger name %q is invalid", name)
	}

	td, ok := m.triggers.Get(name)
	if !ok {
		return nil, entityNotFound("Trigger not found: %s", name)
	}

	return td, nil
}

// GetTrigger returns a deep copy of a trigger.
func (m *Mock) GetTrigger(_ context.Context, name string) (*driver.Trigger, error) {
	td, err := m.getTriggerData(name)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	out := copyTrigger(td.trigger)

	return &out, nil
}

// UpdateTrigger replaces a trigger's mutable fields, returning the updated copy.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateTrigger(_ context.Context, name string, t driver.Trigger) (*driver.Trigger, error) {
	td, err := m.getTriggerData(name)
	if err != nil {
		return nil, err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	created := td.trigger.CreationTime
	state := td.trigger.State
	t.Name = name
	t.CreationTime = created
	t.State = state
	td.trigger = copyTrigger(t)

	out := copyTrigger(td.trigger)

	return &out, nil
}

// DeleteTrigger removes a trigger, returning its name.
func (m *Mock) DeleteTrigger(_ context.Context, name string) (string, error) {
	if _, err := m.getTriggerData(name); err != nil {
		return "", err
	}

	m.triggers.Delete(name)

	return name, nil
}

// GetTriggers lists triggers with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetTriggers(_ context.Context, page driver.TablePagination) ([]driver.Trigger, string, error) {
	keys := sortedKeys(m.triggers.Keys())
	all := make([]driver.Trigger, 0, len(keys))

	for _, key := range keys {
		td, ok := m.triggers.Get(key)
		if !ok {
			continue
		}

		td.mu.RLock()
		all = append(all, copyTrigger(td.trigger))
		td.mu.RUnlock()
	}

	return paginate(all, page)
}

// ListTriggers returns trigger names with pagination.
//
//nolint:gocritic // unnamedResult: thin pass-through to paginate; names add no clarity
func (m *Mock) ListTriggers(_ context.Context, page driver.TablePagination) ([]string, string, error) {
	return paginate(sortedKeys(m.triggers.Keys()), page)
}

// StartTrigger activates a trigger, returning its name.
func (m *Mock) StartTrigger(_ context.Context, name string) (string, error) {
	td, err := m.getTriggerData(name)
	if err != nil {
		return "", err
	}

	td.mu.Lock()
	td.trigger.State = driver.TriggerActivated
	td.mu.Unlock()

	return name, nil
}

// StopTrigger deactivates a trigger, returning its name.
func (m *Mock) StopTrigger(_ context.Context, name string) (string, error) {
	td, err := m.getTriggerData(name)
	if err != nil {
		return "", err
	}

	td.mu.Lock()
	td.trigger.State = driver.TriggerDeactivated
	td.mu.Unlock()

	return name, nil
}

// BatchGetTriggers returns the found triggers and the names that did not exist.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) BatchGetTriggers(_ context.Context, names []string) ([]driver.Trigger, []string, error) {
	if len(names) > maxBatchGet {
		return nil, nil, invalidInput("cannot request more than %d triggers", maxBatchGet)
	}

	found := make([]driver.Trigger, 0, len(names))

	var notFound []string

	for _, n := range names {
		t, err := m.GetTrigger(context.Background(), n)
		if err != nil {
			notFound = append(notFound, n)

			continue
		}

		found = append(found, *t)
	}

	return found, notFound, nil
}
