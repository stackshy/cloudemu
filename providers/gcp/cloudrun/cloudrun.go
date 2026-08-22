// Package cloudrun provides an in-memory backend for GCP Cloud Run Jobs
// (run-to-completion). It satisfies services/cloudrun/driver.CloudRun so the
// Cloud Run Admin API v2 wire handler (server/gcp/cloudrun) serves real
// google.cloud.run.v2 clients against it.
//
// A job stores a container template; RunJob creates an Execution and runs the
// template's containers. When a config.ContainerEngine is wired, RunJob runs
// the containers for real (RunToCompletion) and reflects their true exit codes
// and captured output into the execution; without an engine the execution is a
// synthetic success, keeping the emulator in-memory and dependency-free.
package cloudrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// Compile-time check that Mock implements driver.CloudRun.
var _ driver.CloudRun = (*Mock)(nil)

const (
	launchStageGA    = "GA"
	condReady        = "Ready"
	condCompleted    = "Completed"
	stateSucceeded   = "CONDITION_SUCCEEDED"
	stateFailed      = "CONDITION_FAILED"
	taskSucceeded    = "Succeeded"
	taskFailed       = "Failed"
	defaultTaskCount = 1
	execSuffixBytes  = 4 // 8 hex chars, matching Cloud Run's execution id suffix
	uidBytes         = 16
)

// Mock is an in-memory Cloud Run Jobs backend.
type Mock struct {
	mu         sync.Mutex
	jobs       *memstore.Store[*driver.Job]
	executions *memstore.Store[*driver.Execution]
	// engineHandles maps a job id to the ContainerEngine handles its executions
	// started. A present, non-empty entry is the engine-backed marker DeleteJob
	// consults to tear down real containers; an absent entry means the job's
	// executions were synthetic (no engine wired).
	engineHandles *memstore.Store[[]string]
	opts          *config.Options
}

// New creates a Cloud Run mock with the given options. opts.ContainerEngine,
// when set, backs job executions with real containers.
func New(opts *config.Options) *Mock {
	return &Mock{
		jobs:          memstore.New[*driver.Job](),
		executions:    memstore.New[*driver.Execution](),
		engineHandles: memstore.New[[]string](),
		opts:          opts,
	}
}

// CreateJob stores a job spec without running it.
func (m *Mock) CreateJob(_ context.Context, cfg driver.JobConfig) (*driver.Job, error) {
	name := lastSegment(cfg.Name)
	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "job name is required")
	}

	taskCount := cfg.TaskCount
	if taskCount <= 0 {
		taskCount = defaultTaskCount
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.jobs.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "job %q already exists", name)
	}

	now := m.opts.Clock.Now()
	job := &driver.Job{
		Name:        name,
		UID:         newID(uidBytes),
		Generation:  1,
		CreateTime:  now,
		UpdateTime:  now,
		LaunchStage: launchStageGA,
		TaskCount:   taskCount,
		Containers:  cloneContainers(cfg.Containers),
		Labels:      cloneMap(cfg.Labels),
		Annotations: cloneMap(cfg.Annotations),
		Conditions:  []driver.Condition{{Type: condReady, State: stateSucceeded, Reason: "Ready"}},
	}

	m.jobs.Set(name, job)

	return cloneJob(job), nil
}

// GetJob returns a job by id or fully qualified name.
func (m *Mock) GetJob(_ context.Context, name string) (*driver.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs.Get(lastSegment(name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job %q not found", lastSegment(name))
	}

	return cloneJob(job), nil
}

// ListJobs returns every stored job.
func (m *Mock) ListJobs(_ context.Context) ([]driver.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs := m.jobs.SortedValues()
	out := make([]driver.Job, 0, len(jobs))

	for _, j := range jobs {
		out = append(out, *cloneJob(j))
	}

	return out, nil
}

// DeleteJob removes a job, its executions, and stops any real containers those
// executions started on a configured ContainerEngine.
func (m *Mock) DeleteJob(ctx context.Context, name string) error {
	id := lastSegment(name)

	m.mu.Lock()

	if !m.jobs.Has(id) {
		m.mu.Unlock()

		return cerrors.Newf(cerrors.NotFound, "job %q not found", id)
	}

	handles, _ := m.engineHandles.Get(id)

	m.jobs.Delete(id)
	m.engineHandles.Delete(id)

	for _, key := range m.executions.Keys() {
		if exec, ok := m.executions.Get(key); ok && exec.JobName == id {
			m.executions.Delete(key)
		}
	}

	m.mu.Unlock()

	m.stopHandles(ctx, handles)

	return nil
}

// GetExecution returns an execution by id or fully qualified name.
func (m *Mock) GetExecution(_ context.Context, name string) (*driver.Execution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	exec, ok := m.executions.Get(lastSegment(name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "execution %q not found", lastSegment(name))
	}

	return cloneExecution(exec), nil
}

// lastSegment returns the trailing path segment of a resource name, so callers
// may pass either a bare id or a projects/…/jobs/{id} resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// newID returns a random lowercase-hex identifier of n bytes.
func newID(n int) string {
	b := make([]byte, n)
	// A read error leaves b zeroed, which still encodes to a valid (all-zero)
	// hex id — good enough for an in-memory emulator that never returns error here.
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

func cloneContainers(in []driver.Container) []driver.Container {
	if in == nil {
		return nil
	}

	out := make([]driver.Container, len(in))
	for i := range in {
		out[i] = driver.Container{
			Name:    in[i].Name,
			Image:   in[i].Image,
			Command: append([]string(nil), in[i].Command...),
			Args:    append([]string(nil), in[i].Args...),
			Env:     cloneMap(in[i].Env),
		}
	}

	return out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func cloneJob(j *driver.Job) *driver.Job {
	cp := *j
	cp.Containers = cloneContainers(j.Containers)
	cp.Labels = cloneMap(j.Labels)
	cp.Annotations = cloneMap(j.Annotations)
	cp.Conditions = append([]driver.Condition(nil), j.Conditions...)

	return &cp
}

func cloneExecution(e *driver.Execution) *driver.Execution {
	cp := *e
	cp.Containers = cloneContainers(e.Containers)
	cp.Conditions = append([]driver.Condition(nil), e.Conditions...)
	cp.Tasks = make([]driver.Task, len(e.Tasks))

	for i := range e.Tasks {
		cp.Tasks[i] = e.Tasks[i]
		cp.Tasks[i].Containers = append([]driver.ContainerStatus(nil), e.Tasks[i].Containers...)
	}

	return &cp
}
