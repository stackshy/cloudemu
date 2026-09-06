// Package driver defines the minimal interface an in-memory GCP Cloud Scheduler
// backend must implement. Cloud Scheduler is GCP-only, so there is a single
// provider implementation (providers/gcp/scheduler) rather than the usual three;
// the interface still lives here so the wire handler (server/gcp/scheduler)
// depends on an abstraction rather than the concrete mock.
//
// Scope is the job control plane: create/get/list/patch/delete plus the
// pause/resume/run custom verbs. A Job stores a cron schedule and exactly one
// target (HTTP, Pub/Sub, or App Engine); actually firing the job — HTTP
// delivery, Pub/Sub publish, App Engine routing, OAuth/OIDC token minting — is
// out of scope. The target config (including token config) is stored and echoed
// verbatim so it round-trips, it is simply never dispatched.
package driver

import (
	"context"
	"time"
)

// Job state enum names (google.cloud.scheduler.v1.Job.State). State is
// output-only: a job is ENABLED on create and toggled by the pause/resume verbs.
const (
	StateUnspecified = "STATE_UNSPECIFIED"
	StateEnabled     = "ENABLED"
	StatePaused      = "PAUSED"
	StateDisabled    = "DISABLED"
	StateUpdateFail  = "UPDATE_FAILED"
)

// Scheduler is the control-plane surface for Cloud Scheduler jobs. Names are
// fully-qualified resource names (projects/{p}/locations/{l}/jobs/{j}); ListJobs
// takes the parent (projects/{p}/locations/{l}).
type Scheduler interface {
	// CreateJob stores a job. cfg.Name is the full resource name; the job is
	// created ENABLED with retry/attemptDeadline defaults filled in.
	CreateJob(ctx context.Context, cfg JobConfig) (*Job, error)

	// GetJob returns a job by its full resource name.
	GetJob(ctx context.Context, name string) (*Job, error)

	// ListJobs returns every job under the given parent
	// (projects/{p}/locations/{l}), in deterministic name order.
	ListJobs(ctx context.Context, parent string) ([]Job, error)

	// PatchJob applies a field-mask update to an existing job and returns it.
	// Only the fields named in mask are replaced; an empty mask replaces every
	// field present in cfg.
	PatchJob(ctx context.Context, cfg JobConfig, mask []string) (*Job, error)

	// DeleteJob removes a job by its full resource name.
	DeleteJob(ctx context.Context, name string) error

	// PauseJob transitions a job to PAUSED and returns it.
	PauseJob(ctx context.Context, name string) (*Job, error)

	// ResumeJob transitions a job to ENABLED and returns it.
	ResumeJob(ctx context.Context, name string) (*Job, error)

	// RunJob force-runs a job now. Dispatch is out of scope, so this records the
	// attempt time and returns the job unchanged otherwise.
	RunJob(ctx context.Context, name string) (*Job, error)
}

// Job is a stored Cloud Scheduler job. Exactly one of HTTPTarget, PubsubTarget,
// AppEngineHTTPTarget is set.
type Job struct {
	Name        string
	Description string
	Schedule    string
	TimeZone    string

	HTTPTarget          *HTTPTarget
	PubsubTarget        *PubsubTarget
	AppEngineHTTPTarget *AppEngineHTTPTarget

	RetryConfig     *RetryConfig
	AttemptDeadline string
	State           string

	ScheduleTime    time.Time
	LastAttemptTime time.Time
	UserUpdateTime  time.Time
}

// HTTPTarget is an HTTP request target. Body is raw bytes (base64 on the wire).
type HTTPTarget struct {
	URI        string
	HTTPMethod string
	Headers    map[string]string
	Body       []byte
	OAuthToken *OAuthToken
	OidcToken  *OidcToken
}

// OAuthToken is the OAuth token config for an HTTP target. Stored and echoed
// only; never minted.
type OAuthToken struct {
	ServiceAccountEmail string
	Scope               string
}

// OidcToken is the OIDC token config for an HTTP target. Stored and echoed
// only; never minted.
type OidcToken struct {
	ServiceAccountEmail string
	Audience            string
}

// PubsubTarget is a Pub/Sub publish target. Data is raw bytes (base64 on the
// wire).
type PubsubTarget struct {
	TopicName  string
	Data       []byte
	Attributes map[string]string
}

// AppEngineHTTPTarget is an App Engine request target. Body is raw bytes
// (base64 on the wire).
type AppEngineHTTPTarget struct {
	HTTPMethod       string
	AppEngineRouting *AppEngineRouting
	RelativeURI      string
	Headers          map[string]string
	Body             []byte
}

// AppEngineRouting selects the App Engine service/version/instance a target
// dispatches to.
type AppEngineRouting struct {
	Service  string
	Version  string
	Instance string
	Host     string
}

// RetryConfig is the retry policy for a job's execution attempts.
type RetryConfig struct {
	RetryCount         int32
	MaxRetryDuration   string
	MinBackoffDuration string
	MaxBackoffDuration string
	MaxDoublings       int32
}

// JobConfig is the input to CreateJob/PatchJob. It mirrors the mutable subset
// of Job (state and the output-only timestamps are managed by the backend).
type JobConfig struct {
	Name        string
	Description string
	Schedule    string
	TimeZone    string

	HTTPTarget          *HTTPTarget
	PubsubTarget        *PubsubTarget
	AppEngineHTTPTarget *AppEngineHTTPTarget

	RetryConfig     *RetryConfig
	AttemptDeadline string
}
