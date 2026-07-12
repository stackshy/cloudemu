package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// --- Metadata stores ---

func (m *Mock) CreateMetadataStore(_ context.Context, location, storeID string) (*driver.Operation, *driver.MetadataStore, error) {
	name := m.resName(location, "metadataStores", orID(storeID, m.newID()))
	s := &driver.MetadataStore{Name: name, CreateTime: m.now()}
	m.metadataStores.Set(name, s)

	out := *s

	return m.doneOp(location, name), &out, nil
}

func (m *Mock) GetMetadataStore(_ context.Context, name string) (*driver.MetadataStore, error) {
	s, ok := m.metadataStores.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "metadata store %q not found", name)
	}

	out := *s

	return &out, nil
}

func (m *Mock) ListMetadataStores(_ context.Context, location string) ([]driver.MetadataStore, error) {
	out := make([]driver.MetadataStore, 0)

	for _, s := range m.metadataStores.All() {
		if location == "" || locationOf(s.Name) == location {
			out = append(out, *s)
		}
	}

	return out, nil
}

func (m *Mock) DeleteMetadataStore(_ context.Context, name string) (*driver.Operation, error) {
	if !m.metadataStores.Has(name) {
		return nil, errors.Newf(errors.NotFound, "metadata store %q not found", name)
	}

	m.metadataStores.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Tensorboards ---

func (m *Mock) CreateTensorboard(_ context.Context, location, displayName string) (*driver.Operation, *driver.Tensorboard, error) {
	name := m.resName(location, "tensorboards", m.newID())
	tb := &driver.Tensorboard{Name: name, DisplayName: displayName, CreateTime: m.now()}
	m.tensorboards.Set(name, tb)

	out := *tb

	return m.doneOp(location, name), &out, nil
}

func (m *Mock) GetTensorboard(_ context.Context, name string) (*driver.Tensorboard, error) {
	tb, ok := m.tensorboards.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "tensorboard %q not found", name)
	}

	out := *tb

	return &out, nil
}

func (m *Mock) ListTensorboards(_ context.Context, location string) ([]driver.Tensorboard, error) {
	out := make([]driver.Tensorboard, 0)

	for _, tb := range m.tensorboards.All() {
		if location == "" || locationOf(tb.Name) == location {
			out = append(out, *tb)
		}
	}

	return out, nil
}

func (m *Mock) DeleteTensorboard(_ context.Context, name string) (*driver.Operation, error) {
	if !m.tensorboards.Has(name) {
		return nil, errors.Newf(errors.NotFound, "tensorboard %q not found", name)
	}

	m.tensorboards.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Schedules ---

func (m *Mock) CreateSchedule(_ context.Context, location, displayName, cron string) (*driver.Schedule, error) {
	name := m.resName(location, "schedules", m.newID())
	s := &driver.Schedule{Name: name, DisplayName: displayName, Cron: cron, State: "ACTIVE", CreateTime: m.now()}
	m.schedules.Set(name, s)

	out := *s

	return &out, nil
}

func (m *Mock) GetSchedule(_ context.Context, name string) (*driver.Schedule, error) {
	s, ok := m.schedules.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "schedule %q not found", name)
	}

	out := *s

	return &out, nil
}

func (m *Mock) ListSchedules(_ context.Context, location string) ([]driver.Schedule, error) {
	out := make([]driver.Schedule, 0)

	for _, s := range m.schedules.All() {
		if location == "" || locationOf(s.Name) == location {
			out = append(out, *s)
		}
	}

	return out, nil
}

func (m *Mock) PauseSchedule(_ context.Context, name string) error {
	return m.setScheduleState(name, "ACTIVE", "PAUSED")
}

func (m *Mock) ResumeSchedule(_ context.Context, name string) error {
	return m.setScheduleState(name, "PAUSED", "ACTIVE")
}

// setScheduleState copy-then-Sets a schedule to target only from the required
// source state, rejecting illegal transitions like the real API.
func (m *Mock) setScheduleState(name, from, target string) error {
	s, ok := m.schedules.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "schedule %q not found", name)
	}

	if s.State != from {
		return errors.Newf(errors.FailedPrecondition,
			"schedule %q is %s; cannot transition to %s", name, s.State, target)
	}

	updated := *s
	updated.State = target
	m.schedules.Set(name, &updated)

	return nil
}

func (m *Mock) DeleteSchedule(_ context.Context, name string) (*driver.Operation, error) {
	if !m.schedules.Has(name) {
		return nil, errors.Newf(errors.NotFound, "schedule %q not found", name)
	}

	m.schedules.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Notebook runtime templates ---

func (m *Mock) CreateNotebookRuntimeTemplate(
	_ context.Context, location, displayName, machineType string,
) (*driver.Operation, *driver.NotebookRuntimeTemplate, error) {
	name := m.resName(location, "notebookRuntimeTemplates", m.newID())
	t := &driver.NotebookRuntimeTemplate{Name: name, DisplayName: displayName, MachineType: machineType, CreateTime: m.now()}
	m.nbTemplates.Set(name, t)

	out := *t

	return m.doneOp(location, name), &out, nil
}

func (m *Mock) GetNotebookRuntimeTemplate(_ context.Context, name string) (*driver.NotebookRuntimeTemplate, error) {
	t, ok := m.nbTemplates.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "notebook runtime template %q not found", name)
	}

	out := *t

	return &out, nil
}

func (m *Mock) ListNotebookRuntimeTemplates(_ context.Context, location string) ([]driver.NotebookRuntimeTemplate, error) {
	out := make([]driver.NotebookRuntimeTemplate, 0)

	for _, t := range m.nbTemplates.All() {
		if location == "" || locationOf(t.Name) == location {
			out = append(out, *t)
		}
	}

	return out, nil
}

func (m *Mock) DeleteNotebookRuntimeTemplate(_ context.Context, name string) (*driver.Operation, error) {
	if !m.nbTemplates.Has(name) {
		return nil, errors.Newf(errors.NotFound, "notebook runtime template %q not found", name)
	}

	m.nbTemplates.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Notebook runtimes ---

func (m *Mock) AssignNotebookRuntime(_ context.Context, location, displayName string) (*driver.Operation, *driver.NotebookRuntime, error) {
	name := m.resName(location, "notebookRuntimes", m.newID())
	nr := &driver.NotebookRuntime{Name: name, DisplayName: displayName, RuntimeState: "RUNNING", CreateTime: m.now()}
	m.nbRuntimes.Set(name, nr)

	out := *nr

	return m.doneOp(location, name), &out, nil
}

func (m *Mock) GetNotebookRuntime(_ context.Context, name string) (*driver.NotebookRuntime, error) {
	nr, ok := m.nbRuntimes.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "notebook runtime %q not found", name)
	}

	out := *nr

	return &out, nil
}

func (m *Mock) ListNotebookRuntimes(_ context.Context, location string) ([]driver.NotebookRuntime, error) {
	out := make([]driver.NotebookRuntime, 0)

	for _, nr := range m.nbRuntimes.All() {
		if location == "" || locationOf(nr.Name) == location {
			out = append(out, *nr)
		}
	}

	return out, nil
}

func (m *Mock) StartNotebookRuntime(_ context.Context, name string) (*driver.Operation, error) {
	return m.setRuntimeState(name, "STOPPED", "RUNNING")
}

func (m *Mock) StopNotebookRuntime(_ context.Context, name string) (*driver.Operation, error) {
	return m.setRuntimeState(name, "RUNNING", "STOPPED")
}

// setRuntimeState copy-then-Sets a notebook runtime to target only from the
// required source state, rejecting illegal transitions like the real API.
func (m *Mock) setRuntimeState(name, from, target string) (*driver.Operation, error) {
	nr, ok := m.nbRuntimes.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "notebook runtime %q not found", name)
	}

	if nr.RuntimeState != from {
		return nil, errors.Newf(errors.FailedPrecondition,
			"notebook runtime %q is %s; cannot transition to %s", name, nr.RuntimeState, target)
	}

	updated := *nr
	updated.RuntimeState = target
	m.nbRuntimes.Set(name, &updated)

	return m.doneOp(locationOf(name), name), nil
}

func (m *Mock) DeleteNotebookRuntime(_ context.Context, name string) (*driver.Operation, error) {
	if !m.nbRuntimes.Has(name) {
		return nil, errors.Newf(errors.NotFound, "notebook runtime %q not found", name)
	}

	m.nbRuntimes.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}
