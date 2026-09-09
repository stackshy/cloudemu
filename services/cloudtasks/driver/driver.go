// Package driver defines the minimal interface an in-memory GCP Cloud Tasks
// backend must implement. Cloud Tasks is GCP-only, so there is a single provider
// implementation (providers/gcp/cloudtasks) rather than the usual three; the
// interface still lives here so the wire handler (server/gcp/cloudtasks) depends
// on an abstraction rather than the concrete mock.
//
// Scope is the queue control plane: create/get/list/patch/delete plus the
// pause/resume/purge custom verbs and the getIamPolicy/setIamPolicy/
// testIamPermissions IAM methods. A Queue stores its rate limits, retry policy,
// routing and logging config; task-level operations (CreateTask, RunTask,
// ListTasks, …) and real task dispatch/execution are out of scope. The queue's
// httpTarget config is stored and echoed verbatim so it round-trips, it is
// simply never used to dispatch a task.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Queue state enum names (google.cloud.tasks.v2.Queue.State). State is
// output-only: a queue is RUNNING on create and toggled by the pause/resume
// verbs. The ordinals are STATE_UNSPECIFIED=0, RUNNING=1, PAUSED=2, DISABLED=3.
const (
	StateUnspecified = "STATE_UNSPECIFIED"
	StateRunning     = "RUNNING"
	StatePaused      = "PAUSED"
	StateDisabled    = "DISABLED"
)

// Queues is the control-plane surface for Cloud Tasks queues. Names are
// fully-qualified resource names (projects/{p}/locations/{l}/queues/{q});
// ListQueues takes the parent (projects/{p}/locations/{l}).
type Queues interface {
	// CreateQueue stores a queue. cfg.Name is the full resource name; the queue
	// is created RUNNING with rateLimits/retryConfig defaults filled in.
	CreateQueue(ctx context.Context, cfg QueueConfig) (*Queue, error)

	// GetQueue returns a queue by its full resource name.
	GetQueue(ctx context.Context, name string) (*Queue, error)

	// ListQueues returns every queue under the given parent
	// (projects/{p}/locations/{l}), in deterministic name order.
	ListQueues(ctx context.Context, parent string) ([]Queue, error)

	// PatchQueue applies a field-mask update to an existing queue and returns it.
	// Only the fields named in mask are replaced; an empty mask replaces every
	// field present in cfg.
	PatchQueue(ctx context.Context, cfg QueueConfig, mask []string) (*Queue, error)

	// DeleteQueue removes a queue by its full resource name.
	DeleteQueue(ctx context.Context, name string) error

	// PauseQueue transitions a queue to PAUSED and returns it.
	PauseQueue(ctx context.Context, name string) (*Queue, error)

	// ResumeQueue transitions a queue to RUNNING and returns it.
	ResumeQueue(ctx context.Context, name string) (*Queue, error)

	// PurgeQueue purges a queue's tasks. Tasks are out of scope, so this records
	// the purge time and returns the queue.
	PurgeQueue(ctx context.Context, name string) (*Queue, error)

	// GetIamPolicy returns the queue's stored IAM policy (an empty, versioned
	// policy when none was set). The queue must exist.
	GetIamPolicy(ctx context.Context, name string) (*IAMPolicy, error)

	// SetIamPolicy stores the queue's IAM policy and returns it with a refreshed
	// etag. The queue must exist.
	SetIamPolicy(ctx context.Context, name string, policy IAMPolicy) (*IAMPolicy, error)

	// TestIamPermissions echoes back the requested permissions (CloudEmu does not
	// enforce IAM). The queue must exist.
	TestIamPermissions(ctx context.Context, name string, permissions []string) ([]string, error)
}

// Queue is a stored Cloud Tasks queue.
type Queue struct {
	Name string

	AppEngineRoutingOverride *AppEngineRouting
	RateLimits               *RateLimits
	RetryConfig              *RetryConfig
	StackdriverLoggingConfig *StackdriverLoggingConfig

	// HTTPTarget is the queue-level HTTP target (HTTP queues). It is stored and
	// echoed verbatim as opaque JSON so it round-trips; it is never used to
	// dispatch a task.
	HTTPTarget json.RawMessage

	State     string
	PurgeTime time.Time

	// IAMPolicy is the queue's stored IAM policy, nil until first set. It is not
	// part of the Queue wire shape; it is served by the getIamPolicy method.
	IAMPolicy *IAMPolicy
}

// RateLimits is a queue's dispatch rate config. MaxBurstSize is output-only and
// computed from MaxDispatchesPerSecond by the backend.
type RateLimits struct {
	MaxDispatchesPerSecond  float64
	MaxBurstSize            int64
	MaxConcurrentDispatches int64
}

// RetryConfig is a queue's task-retry policy. Durations are proto Duration
// strings (seconds followed by "s").
type RetryConfig struct {
	MaxAttempts      int64
	MaxRetryDuration string
	MinBackoff       string
	MaxBackoff       string
	MaxDoublings     int64
}

// AppEngineRouting selects the App Engine service/version/instance an App Engine
// task dispatches to.
type AppEngineRouting struct {
	Service  string
	Version  string
	Instance string
	Host     string
}

// StackdriverLoggingConfig configures the fraction of operations logged.
type StackdriverLoggingConfig struct {
	SamplingRatio float64
}

// IAMPolicy is a GCP IAM policy (the getIamPolicy/setIamPolicy resource).
type IAMPolicy struct {
	Version  int
	Bindings []IAMBinding
	Etag     string
}

// IAMBinding associates a role with a list of members.
type IAMBinding struct {
	Role    string
	Members []string
}

// QueueConfig is the input to CreateQueue/PatchQueue. It mirrors the mutable
// subset of Queue (state and purgeTime are managed by the backend, and
// maxBurstSize is recomputed by the backend from maxDispatchesPerSecond).
type QueueConfig struct {
	Name string

	AppEngineRoutingOverride *AppEngineRouting
	RateLimits               *RateLimits
	RetryConfig              *RetryConfig
	StackdriverLoggingConfig *StackdriverLoggingConfig
	HTTPTarget               json.RawMessage
}
