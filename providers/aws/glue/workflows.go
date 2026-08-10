package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// workflowData is a workflow plus its own lock.
type workflowData struct {
	workflow driver.Workflow
	mu       sync.RWMutex
}

// workflowRunData is a single workflow run plus its own lock, keyed
// "<workflowName>/<runID>".
type workflowRunData struct {
	run driver.WorkflowRun
	mu  sync.RWMutex
}

// CreateWorkflow creates a workflow, atomically, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateWorkflow(_ context.Context, w driver.Workflow) (string, error) {
	if !validName(w.Name) {
		return "", invalidInput("workflow name %q is invalid", w.Name)
	}

	now := m.now()
	w.CreatedOn = now
	w.LastModifiedOn = now
	stored := copyWorkflow(w)

	if !m.workflows.SetIfAbsent(w.Name, &workflowData{workflow: stored}) {
		return "", alreadyExists("Workflow already exists: %s", w.Name)
	}

	return w.Name, nil
}

func (m *Mock) getWorkflowData(name string) (*workflowData, error) {
	if !validName(name) {
		return nil, invalidInput("workflow name %q is invalid", name)
	}

	wd, ok := m.workflows.Get(name)
	if !ok {
		return nil, entityNotFound("Workflow not found: %s", name)
	}

	return wd, nil
}

// GetWorkflow returns a deep copy of a workflow.
func (m *Mock) GetWorkflow(_ context.Context, name string) (*driver.Workflow, error) {
	wd, err := m.getWorkflowData(name)
	if err != nil {
		return nil, err
	}

	wd.mu.RLock()
	defer wd.mu.RUnlock()

	out := copyWorkflow(wd.workflow)

	return &out, nil
}

// UpdateWorkflow replaces a workflow's mutable fields, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateWorkflow(_ context.Context, name string, w driver.Workflow) (string, error) {
	wd, err := m.getWorkflowData(name)
	if err != nil {
		return "", err
	}

	wd.mu.Lock()
	defer wd.mu.Unlock()

	created := wd.workflow.CreatedOn
	wd.workflow = copyWorkflow(w)
	wd.workflow.Name = name
	wd.workflow.CreatedOn = created
	wd.workflow.LastModifiedOn = m.now()

	return name, nil
}

// DeleteWorkflow removes a workflow and its runs, returning its name.
func (m *Mock) DeleteWorkflow(_ context.Context, name string) (string, error) {
	if _, err := m.getWorkflowData(name); err != nil {
		return "", err
	}

	m.workflows.Delete(name)

	prefix := name + keySep
	for _, key := range m.workflowRuns.Keys() {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			m.workflowRuns.Delete(key)
		}
	}

	return name, nil
}

// ListWorkflows returns workflow names with pagination.
//
//nolint:gocritic // unnamedResult: thin pass-through to paginate; names add no clarity
func (m *Mock) ListWorkflows(_ context.Context, page driver.TablePagination) ([]string, string, error) {
	return paginate(sortedKeys(m.workflows.Keys()), page)
}

// BatchGetWorkflows returns the found workflows and the names that did not exist.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) BatchGetWorkflows(_ context.Context, names []string) ([]driver.Workflow, []string, error) {
	if len(names) > maxBatchGet {
		return nil, nil, invalidInput("cannot request more than %d workflows", maxBatchGet)
	}

	found := make([]driver.Workflow, 0, len(names))

	var notFound []string

	for _, n := range names {
		w, err := m.GetWorkflow(context.Background(), n)
		if err != nil {
			notFound = append(notFound, n)

			continue
		}

		found = append(found, *w)
	}

	return found, notFound, nil
}

// StartWorkflowRun starts a run. There is no real orchestration engine, so the
// run completes synchronously (COMPLETED). Returns the run ID.
func (m *Mock) StartWorkflowRun(_ context.Context, name string) (string, error) {
	wd, err := m.getWorkflowData(name)
	if err != nil {
		return "", err
	}

	wd.mu.RLock()
	props := copyTags(wd.workflow.DefaultRunProperties)
	wd.mu.RUnlock()

	now := m.now()
	runID := idgen.GenerateID("wr_")
	run := driver.WorkflowRun{
		Name:          name,
		WorkflowRunID: runID,
		Status:        driver.WorkflowRunCompleted,
		StartedOn:     now,
		CompletedOn:   now,
		RunProperties: props,
	}

	m.workflowRuns.Set(nameKey(name, runID), &workflowRunData{run: run})

	return runID, nil
}

func (m *Mock) getWorkflowRunData(name, runID string) (*workflowRunData, error) {
	rd, ok := m.workflowRuns.Get(nameKey(name, runID))
	if !ok {
		return nil, entityNotFound("WorkflowRun not found: %s", runID)
	}

	return rd, nil
}

// GetWorkflowRun returns a deep copy of a workflow run.
func (m *Mock) GetWorkflowRun(_ context.Context, name, runID string) (*driver.WorkflowRun, error) {
	rd, err := m.getWorkflowRunData(name, runID)
	if err != nil {
		return nil, err
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	out := copyWorkflowRun(rd.run)

	return &out, nil
}

// GetWorkflowRuns lists a workflow's runs with pagination.
//
//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (m *Mock) GetWorkflowRuns(
	_ context.Context, name string, page driver.TablePagination,
) ([]driver.WorkflowRun, string, error) {
	if _, err := m.getWorkflowData(name); err != nil {
		return nil, "", err
	}

	prefix := name + keySep
	keys := sortedKeys(m.workflowRuns.Keys())
	all := make([]driver.WorkflowRun, 0, len(keys))

	for _, key := range keys {
		if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
			continue
		}

		rd, ok := m.workflowRuns.Get(key)
		if !ok {
			continue
		}

		rd.mu.RLock()
		all = append(all, copyWorkflowRun(rd.run))
		rd.mu.RUnlock()
	}

	return paginate(all, page)
}

// StopWorkflowRun is a no-op success for a terminal (COMPLETED) run.
func (m *Mock) StopWorkflowRun(_ context.Context, name, runID string) error {
	_, err := m.getWorkflowRunData(name, runID)

	return err
}

// ResumeWorkflowRun starts a fresh run to represent the resume, returning its ID.
func (m *Mock) ResumeWorkflowRun(_ context.Context, name, runID string, _ []string) (string, error) {
	if _, err := m.getWorkflowRunData(name, runID); err != nil {
		return "", err
	}

	return m.StartWorkflowRun(context.Background(), name)
}

// GetWorkflowRunProperties returns a copy of a run's properties.
func (m *Mock) GetWorkflowRunProperties(_ context.Context, name, runID string) (map[string]string, error) {
	rd, err := m.getWorkflowRunData(name, runID)
	if err != nil {
		return nil, err
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	return copyTags(rd.run.RunProperties), nil
}

// PutWorkflowRunProperties replaces a run's properties.
func (m *Mock) PutWorkflowRunProperties(_ context.Context, name, runID string, props map[string]string) error {
	rd, err := m.getWorkflowRunData(name, runID)
	if err != nil {
		return err
	}

	rd.mu.Lock()
	rd.run.RunProperties = copyTags(props)
	rd.mu.Unlock()

	return nil
}
