package cloudtasks

import (
	"encoding/json"
	"time"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

// queueJSON is the wire shape of a Cloud Tasks Queue. httpTarget is carried as
// opaque JSON (json.RawMessage) so an HTTP-queue config round-trips verbatim
// without the control plane modeling the full HttpTarget/UriOverride grammar.
type queueJSON struct {
	Name string `json:"name,omitempty"`

	AppEngineRoutingOverride *appEngineRoutingJSON   `json:"appEngineRoutingOverride,omitempty"`
	RateLimits               *rateLimitsJSON         `json:"rateLimits,omitempty"`
	RetryConfig              *retryConfigJSON        `json:"retryConfig,omitempty"`
	StackdriverLoggingConfig *stackdriverLoggingJSON `json:"stackdriverLoggingConfig,omitempty"`
	HTTPTarget               json.RawMessage         `json:"httpTarget,omitempty"`

	State     string `json:"state,omitempty"`
	PurgeTime string `json:"purgeTime,omitempty"`
}

type rateLimitsJSON struct {
	MaxDispatchesPerSecond  float64 `json:"maxDispatchesPerSecond,omitempty"`
	MaxBurstSize            int64   `json:"maxBurstSize,omitempty"`
	MaxConcurrentDispatches int64   `json:"maxConcurrentDispatches,omitempty"`
}

type retryConfigJSON struct {
	MaxAttempts      int64  `json:"maxAttempts,omitempty"`
	MaxRetryDuration string `json:"maxRetryDuration,omitempty"`
	MinBackoff       string `json:"minBackoff,omitempty"`
	MaxBackoff       string `json:"maxBackoff,omitempty"`
	MaxDoublings     int64  `json:"maxDoublings,omitempty"`
}

type appEngineRoutingJSON struct {
	Service  string `json:"service,omitempty"`
	Version  string `json:"version,omitempty"`
	Instance string `json:"instance,omitempty"`
	Host     string `json:"host,omitempty"`
}

type stackdriverLoggingJSON struct {
	SamplingRatio float64 `json:"samplingRatio,omitempty"`
}

type listQueuesResponse struct {
	Queues        []queueJSON `json:"queues,omitempty"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

// iamPolicyJSON is the GCP IAM Policy resource returned by getIamPolicy /
// setIamPolicy.
type iamPolicyJSON struct {
	Version  int              `json:"version,omitempty"`
	Bindings []iamBindingJSON `json:"bindings,omitempty"`
	Etag     string           `json:"etag,omitempty"`
}

type iamBindingJSON struct {
	Role    string   `json:"role"`
	Members []string `json:"members,omitempty"`
}

type setIamPolicyRequest struct {
	Policy iamPolicyJSON `json:"policy"`
}

type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
}

// toDriverConfig converts a decoded wire queue into a driver QueueConfig.
func (q *queueJSON) toDriverConfig(name string) ctdriver.QueueConfig {
	return ctdriver.QueueConfig{
		Name:                     name,
		AppEngineRoutingOverride: q.AppEngineRoutingOverride.toDriver(),
		RateLimits:               q.RateLimits.toDriver(),
		RetryConfig:              q.RetryConfig.toDriver(),
		StackdriverLoggingConfig: q.StackdriverLoggingConfig.toDriver(),
		HTTPTarget:               q.HTTPTarget,
	}
}

func (r *rateLimitsJSON) toDriver() *ctdriver.RateLimits {
	if r == nil {
		return nil
	}

	return &ctdriver.RateLimits{
		MaxDispatchesPerSecond:  r.MaxDispatchesPerSecond,
		MaxBurstSize:            r.MaxBurstSize,
		MaxConcurrentDispatches: r.MaxConcurrentDispatches,
	}
}

func (c *retryConfigJSON) toDriver() *ctdriver.RetryConfig {
	if c == nil {
		return nil
	}

	return &ctdriver.RetryConfig{
		MaxAttempts:      c.MaxAttempts,
		MaxRetryDuration: c.MaxRetryDuration,
		MinBackoff:       c.MinBackoff,
		MaxBackoff:       c.MaxBackoff,
		MaxDoublings:     c.MaxDoublings,
	}
}

func (r *appEngineRoutingJSON) toDriver() *ctdriver.AppEngineRouting {
	if r == nil {
		return nil
	}

	return &ctdriver.AppEngineRouting{Service: r.Service, Version: r.Version, Instance: r.Instance, Host: r.Host}
}

func (l *stackdriverLoggingJSON) toDriver() *ctdriver.StackdriverLoggingConfig {
	if l == nil {
		return nil
	}

	return &ctdriver.StackdriverLoggingConfig{SamplingRatio: l.SamplingRatio}
}

// toQueueJSON renders a driver Queue for the wire.
func toQueueJSON(q *ctdriver.Queue) queueJSON {
	return queueJSON{
		Name:                     q.Name,
		AppEngineRoutingOverride: appEngineRoutingToJSON(q.AppEngineRoutingOverride),
		RateLimits:               rateLimitsToJSON(q.RateLimits),
		RetryConfig:              retryConfigToJSON(q.RetryConfig),
		StackdriverLoggingConfig: loggingConfigToJSON(q.StackdriverLoggingConfig),
		HTTPTarget:               q.HTTPTarget,
		State:                    q.State,
		PurgeTime:                formatTime(q.PurgeTime),
	}
}

func rateLimitsToJSON(r *ctdriver.RateLimits) *rateLimitsJSON {
	if r == nil {
		return nil
	}

	return &rateLimitsJSON{
		MaxDispatchesPerSecond:  r.MaxDispatchesPerSecond,
		MaxBurstSize:            r.MaxBurstSize,
		MaxConcurrentDispatches: r.MaxConcurrentDispatches,
	}
}

func retryConfigToJSON(c *ctdriver.RetryConfig) *retryConfigJSON {
	if c == nil {
		return nil
	}

	return &retryConfigJSON{
		MaxAttempts:      c.MaxAttempts,
		MaxRetryDuration: c.MaxRetryDuration,
		MinBackoff:       c.MinBackoff,
		MaxBackoff:       c.MaxBackoff,
		MaxDoublings:     c.MaxDoublings,
	}
}

func appEngineRoutingToJSON(r *ctdriver.AppEngineRouting) *appEngineRoutingJSON {
	if r == nil {
		return nil
	}

	return &appEngineRoutingJSON{Service: r.Service, Version: r.Version, Instance: r.Instance, Host: r.Host}
}

func loggingConfigToJSON(l *ctdriver.StackdriverLoggingConfig) *stackdriverLoggingJSON {
	if l == nil {
		return nil
	}

	return &stackdriverLoggingJSON{SamplingRatio: l.SamplingRatio}
}

// toPolicyJSON renders a driver IAM policy for the wire.
func toPolicyJSON(pol *ctdriver.IAMPolicy) iamPolicyJSON {
	out := iamPolicyJSON{Version: pol.Version, Etag: pol.Etag}
	for _, b := range pol.Bindings {
		out.Bindings = append(out.Bindings, iamBindingJSON{Role: b.Role, Members: b.Members})
	}

	return out
}

// fromPolicyJSON decodes a wire IAM policy into the driver model.
func fromPolicyJSON(pol iamPolicyJSON) ctdriver.IAMPolicy {
	out := ctdriver.IAMPolicy{Version: pol.Version, Etag: pol.Etag}
	for _, b := range pol.Bindings {
		out.Bindings = append(out.Bindings, ctdriver.IAMBinding{Role: b.Role, Members: b.Members})
	}

	return out
}

// formatTime renders a timestamp as RFC3339 (proto3 JSON), or "" when zero so
// the omitempty output-only field is dropped.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
