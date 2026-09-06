// Package scheduler provides an in-memory backend for GCP Cloud Scheduler
// (cloudscheduler.googleapis.com v1). It satisfies services/scheduler/driver.
// Scheduler so the Cloud Scheduler v1 REST wire handler (server/gcp/scheduler)
// serves real google.golang.org/api/cloudscheduler/v1 clients — and Terraform's
// google provider — against it.
//
// This is the job control plane only: create/get/list/patch/delete plus the
// pause/resume/run verbs. Firing a job (HTTP delivery, Pub/Sub publish, App
// Engine routing, OAuth/OIDC token minting) is out of scope; a job's target and
// token config are stored and echoed verbatim, they are simply never dispatched.
package scheduler

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

// Compile-time check that Mock implements driver.Scheduler.
var _ driver.Scheduler = (*Mock)(nil)

// Job defaults, applied on create so a Get always returns the canonical form a
// real Cloud Scheduler server fills in (otherwise clients drift on unset fields).
const (
	defaultAttemptDeadline = "180s"
	defaultMinBackoff      = "5s"
	defaultMaxBackoff      = "3600s"
	defaultMaxRetryDur     = "0s"
	defaultMaxDoublings    = 5
)

// System headers Cloud Scheduler injects on the request it dispatches. Real
// Cloud Scheduler always returns these on an HTTP/App Engine target (clients —
// notably Terraform's google provider — assume the headers map is present and
// filter these out), so the backend stores and echoes them too.
const (
	headerUserAgent   = "User-Agent"
	headerContentType = "Content-Type"
	userAgentHTTP     = "Google-Cloud-Scheduler"
	userAgentAppng    = "AppEngine-Google; (+http://code.google.com/appengine)"
	contentTypeOctet  = "application/octet-stream"
)

// Mock is an in-memory Cloud Scheduler backend. Jobs are keyed by their full
// resource name (projects/{p}/locations/{l}/jobs/{j}).
type Mock struct {
	mu   sync.Mutex
	jobs *memstore.Store[*driver.Job]
	opts *config.Options
}

// New creates a Cloud Scheduler mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{jobs: memstore.New[*driver.Job](), opts: opts}
}

// CreateJob stores a new job, ENABLED, with retry/attemptDeadline defaults.
//
//nolint:gocritic // hugeParam: cfg is passed by value to satisfy the driver interface.
func (m *Mock) CreateJob(_ context.Context, cfg driver.JobConfig) (*driver.Job, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "job name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.jobs.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "job %q already exists", cfg.Name)
	}

	job := jobFromConfig(cfg)
	job.State = driver.StateEnabled
	job.UserUpdateTime = m.opts.Clock.Now()
	applyDefaults(job)

	m.jobs.Set(cfg.Name, job)

	return cloneJob(job), nil
}

// GetJob returns a job by its full resource name.
func (m *Mock) GetJob(_ context.Context, name string) (*driver.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job %q not found", name)
	}

	return cloneJob(job), nil
}

// ListJobs returns every job under parent (projects/{p}/locations/{l}), in
// deterministic name order.
func (m *Mock) ListJobs(_ context.Context, parent string) ([]driver.Job, error) {
	prefix := strings.TrimSuffix(parent, "/") + "/jobs/"

	m.mu.Lock()
	defer m.mu.Unlock()

	all := m.jobs.SortedValues()
	out := make([]driver.Job, 0, len(all))

	for _, j := range all {
		if strings.HasPrefix(j.Name, prefix) {
			out = append(out, *cloneJob(j))
		}
	}

	return out, nil
}

// PatchJob applies a field-mask update to an existing job.
//
//nolint:gocritic // hugeParam: cfg is passed by value to satisfy the driver interface.
func (m *Mock) PatchJob(_ context.Context, cfg driver.JobConfig, mask []string) (*driver.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job %q not found", cfg.Name)
	}

	applyMask(job, cfg, mask)
	job.UserUpdateTime = m.opts.Clock.Now()
	applyDefaults(job)

	m.jobs.Set(cfg.Name, job)

	return cloneJob(job), nil
}

// DeleteJob removes a job by its full resource name.
func (m *Mock) DeleteJob(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.jobs.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "job %q not found", name)
	}

	m.jobs.Delete(name)

	return nil
}

// PauseJob transitions a job to PAUSED.
func (m *Mock) PauseJob(ctx context.Context, name string) (*driver.Job, error) {
	return m.setState(ctx, name, driver.StatePaused)
}

// ResumeJob transitions a job to ENABLED.
func (m *Mock) ResumeJob(ctx context.Context, name string) (*driver.Job, error) {
	return m.setState(ctx, name, driver.StateEnabled)
}

// setState mutates a job's state and returns the updated job.
func (m *Mock) setState(_ context.Context, name, state string) (*driver.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job %q not found", name)
	}

	job.State = state
	m.jobs.Set(name, job)

	return cloneJob(job), nil
}

// RunJob force-runs a job. Dispatch is out of scope, so it only records the
// attempt time.
func (m *Mock) RunJob(_ context.Context, name string) (*driver.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job %q not found", name)
	}

	job.LastAttemptTime = m.opts.Clock.Now()
	m.jobs.Set(name, job)

	return cloneJob(job), nil
}

// applyDefaults fills the output defaults a real Cloud Scheduler server returns.
func applyDefaults(j *driver.Job) {
	if j.AttemptDeadline == "" {
		j.AttemptDeadline = defaultAttemptDeadline
	}

	injectSystemHeaders(j)

	// Only fill retry defaults when the job actually carries a retry policy.
	// Synthesizing a full default retryConfig for a job that never set one makes
	// clients (Terraform's google provider) see a block they want to remove; a
	// real job with no configured retry policy returns no retryConfig. The
	// individual sub-fields are computed defaults, so a partially-set policy
	// (e.g. only retryCount) is completed here without drift.
	if j.RetryConfig == nil {
		return
	}

	rc := j.RetryConfig
	if rc.MinBackoffDuration == "" {
		rc.MinBackoffDuration = defaultMinBackoff
	}

	if rc.MaxBackoffDuration == "" {
		rc.MaxBackoffDuration = defaultMaxBackoff
	}

	if rc.MaxRetryDuration == "" {
		rc.MaxRetryDuration = defaultMaxRetryDur
	}

	if rc.MaxDoublings == 0 {
		rc.MaxDoublings = defaultMaxDoublings
	}
}

// injectSystemHeaders ensures an HTTP or App Engine target carries the system
// headers Cloud Scheduler adds (a non-nil headers map with User-Agent, plus
// Content-Type when a body is present), without overwriting any header the user
// set. This mirrors the real service and keeps the headers map present for
// clients that assume it always is.
func injectSystemHeaders(j *driver.Job) {
	if t := j.HTTPTarget; t != nil {
		t.Headers = systemHeaders(t.Headers, userAgentHTTP, len(t.Body) > 0)
	}

	if t := j.AppEngineHTTPTarget; t != nil {
		t.Headers = systemHeaders(t.Headers, userAgentAppng, len(t.Body) > 0)
	}
}

// systemHeaders returns headers with User-Agent (and Content-Type when hasBody)
// filled in for any key the caller did not already set.
func systemHeaders(headers map[string]string, userAgent string, hasBody bool) map[string]string {
	if headers == nil {
		headers = map[string]string{}
	}

	if _, ok := headers[headerUserAgent]; !ok {
		headers[headerUserAgent] = userAgent
	}

	if _, ok := headers[headerContentType]; !ok && hasBody {
		headers[headerContentType] = contentTypeOctet
	}

	return headers
}

// jobFromConfig materializes a Job from a create config (deep-copying targets).
//
//nolint:gocritic // hugeParam: cfg mirrors the driver interface's value semantics.
func jobFromConfig(cfg driver.JobConfig) *driver.Job {
	return &driver.Job{
		Name:                cfg.Name,
		Description:         cfg.Description,
		Schedule:            cfg.Schedule,
		TimeZone:            cfg.TimeZone,
		HTTPTarget:          cloneHTTPTarget(cfg.HTTPTarget),
		PubsubTarget:        clonePubsubTarget(cfg.PubsubTarget),
		AppEngineHTTPTarget: cloneAppEngineTarget(cfg.AppEngineHTTPTarget),
		RetryConfig:         cloneRetryConfig(cfg.RetryConfig),
		AttemptDeadline:     cfg.AttemptDeadline,
	}
}
