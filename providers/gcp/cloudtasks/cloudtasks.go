// Package cloudtasks provides an in-memory backend for GCP Cloud Tasks
// (cloudtasks.googleapis.com v2). It satisfies services/cloudtasks/driver.Queues
// so the Cloud Tasks v2 REST wire handler (server/gcp/cloudtasks) serves real
// google.golang.org/api/cloudtasks/v2 clients — and Terraform's google provider
// (google_cloud_tasks_queue) — against it.
//
// This is the queue control plane only: create/get/list/patch/delete plus the
// pause/resume/purge verbs and the IAM methods. Task-level operations and real
// task dispatch/execution are out of scope; a queue's config is stored and
// echoed verbatim so it round-trips.
package cloudtasks

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

// Compile-time check that Mock implements driver.Queues.
var _ driver.Queues = (*Mock)(nil)

// Mock is an in-memory Cloud Tasks backend. Queues are keyed by their full
// resource name (projects/{p}/locations/{l}/queues/{q}).
type Mock struct {
	mu     sync.Mutex
	queues *memstore.Store[*driver.Queue]
	opts   *config.Options
}

// New creates a Cloud Tasks mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{queues: memstore.New[*driver.Queue](), opts: opts}
}

// CreateQueue stores a new queue, RUNNING, with rateLimits/retryConfig defaults.
func (m *Mock) CreateQueue(_ context.Context, cfg driver.QueueConfig) (*driver.Queue, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "queue name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.queues.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "queue %q already exists", cfg.Name)
	}

	q := queueFromConfig(cfg)
	q.State = driver.StateRunning
	applyDefaults(q)

	m.queues.Set(cfg.Name, q)

	return cloneQueue(q), nil
}

// GetQueue returns a queue by its full resource name.
func (m *Mock) GetQueue(_ context.Context, name string) (*driver.Queue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	return cloneQueue(q), nil
}

// ListQueues returns every queue under parent (projects/{p}/locations/{l}), in
// deterministic name order.
func (m *Mock) ListQueues(_ context.Context, parent string) ([]driver.Queue, error) {
	prefix := strings.TrimSuffix(parent, "/") + "/queues/"

	m.mu.Lock()
	defer m.mu.Unlock()

	all := m.queues.SortedValues()
	out := make([]driver.Queue, 0, len(all))

	for _, q := range all {
		if strings.HasPrefix(q.Name, prefix) {
			out = append(out, *cloneQueue(q))
		}
	}

	return out, nil
}

// PatchQueue applies a field-mask update to an existing queue.
func (m *Mock) PatchQueue(_ context.Context, cfg driver.QueueConfig, mask []string) (*driver.Queue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", cfg.Name)
	}

	applyMask(q, cfg, mask)
	applyDefaults(q)

	m.queues.Set(cfg.Name, q)

	return cloneQueue(q), nil
}

// DeleteQueue removes a queue by its full resource name.
func (m *Mock) DeleteQueue(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.queues.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	m.queues.Delete(name)

	return nil
}

// PauseQueue transitions a queue to PAUSED.
func (m *Mock) PauseQueue(ctx context.Context, name string) (*driver.Queue, error) {
	return m.setState(ctx, name, driver.StatePaused)
}

// ResumeQueue transitions a queue to RUNNING.
func (m *Mock) ResumeQueue(ctx context.Context, name string) (*driver.Queue, error) {
	return m.setState(ctx, name, driver.StateRunning)
}

// setState mutates a queue's state and returns the updated queue.
func (m *Mock) setState(_ context.Context, name, state string) (*driver.Queue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	q.State = state
	m.queues.Set(name, q)

	return cloneQueue(q), nil
}

// PurgeQueue purges a queue's tasks. Tasks are out of scope, so it only records
// the purge time.
func (m *Mock) PurgeQueue(_ context.Context, name string) (*driver.Queue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
	}

	q.PurgeTime = m.opts.Clock.Now()
	m.queues.Set(name, q)

	return cloneQueue(q), nil
}
