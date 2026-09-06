package scheduler

import (
	"time"

	scheddriver "github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

// jobJSON is the wire shape of a Cloud Scheduler Job. Body/data bytes fields are
// declared []byte, so encoding/json round-trips them as standard base64 (proto3
// JSON's bytes encoding) verbatim.
type jobJSON struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`

	HTTPTarget          *httpTargetJSON      `json:"httpTarget,omitempty"`
	PubsubTarget        *pubsubTargetJSON    `json:"pubsubTarget,omitempty"`
	AppEngineHTTPTarget *appEngineTargetJSON `json:"appEngineHttpTarget,omitempty"`

	RetryConfig     *retryConfigJSON `json:"retryConfig,omitempty"`
	AttemptDeadline string           `json:"attemptDeadline,omitempty"`
	State           string           `json:"state,omitempty"`

	ScheduleTime    string `json:"scheduleTime,omitempty"`
	LastAttemptTime string `json:"lastAttemptTime,omitempty"`
	UserUpdateTime  string `json:"userUpdateTime,omitempty"`
}

type httpTargetJSON struct {
	URI        string            `json:"uri,omitempty"`
	HTTPMethod string            `json:"httpMethod,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	OAuthToken *oauthTokenJSON   `json:"oauthToken,omitempty"`
	OidcToken  *oidcTokenJSON    `json:"oidcToken,omitempty"`
}

type oauthTokenJSON struct {
	ServiceAccountEmail string `json:"serviceAccountEmail,omitempty"`
	Scope               string `json:"scope,omitempty"`
}

type oidcTokenJSON struct {
	ServiceAccountEmail string `json:"serviceAccountEmail,omitempty"`
	Audience            string `json:"audience,omitempty"`
}

type pubsubTargetJSON struct {
	TopicName  string            `json:"topicName,omitempty"`
	Data       []byte            `json:"data,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type appEngineTargetJSON struct {
	HTTPMethod       string                `json:"httpMethod,omitempty"`
	AppEngineRouting *appEngineRoutingJSON `json:"appEngineRouting,omitempty"`
	RelativeURI      string                `json:"relativeUri,omitempty"`
	Headers          map[string]string     `json:"headers,omitempty"`
	Body             []byte                `json:"body,omitempty"`
}

type appEngineRoutingJSON struct {
	Service  string `json:"service,omitempty"`
	Version  string `json:"version,omitempty"`
	Instance string `json:"instance,omitempty"`
	Host     string `json:"host,omitempty"`
}

type retryConfigJSON struct {
	RetryCount         int32  `json:"retryCount,omitempty"`
	MaxRetryDuration   string `json:"maxRetryDuration,omitempty"`
	MinBackoffDuration string `json:"minBackoffDuration,omitempty"`
	MaxBackoffDuration string `json:"maxBackoffDuration,omitempty"`
	MaxDoublings       int32  `json:"maxDoublings,omitempty"`
}

type listJobsResponse struct {
	Jobs          []jobJSON `json:"jobs,omitempty"`
	NextPageToken string    `json:"nextPageToken,omitempty"`
}

// toDriverConfig converts a decoded wire job into a driver JobConfig, defaulting
// an unset HTTP method to POST (Cloud Scheduler's documented default).
func (j *jobJSON) toDriverConfig(name string) scheddriver.JobConfig {
	return scheddriver.JobConfig{
		Name:                name,
		Description:         j.Description,
		Schedule:            j.Schedule,
		TimeZone:            j.TimeZone,
		HTTPTarget:          j.HTTPTarget.toDriver(),
		PubsubTarget:        j.PubsubTarget.toDriver(),
		AppEngineHTTPTarget: j.AppEngineHTTPTarget.toDriver(),
		RetryConfig:         j.RetryConfig.toDriver(),
		AttemptDeadline:     j.AttemptDeadline,
	}
}

func (t *httpTargetJSON) toDriver() *scheddriver.HTTPTarget {
	if t == nil {
		return nil
	}

	out := &scheddriver.HTTPTarget{
		URI:        t.URI,
		HTTPMethod: defaultMethod(t.HTTPMethod),
		Headers:    t.Headers,
		Body:       t.Body,
	}

	if t.OAuthToken != nil {
		out.OAuthToken = &scheddriver.OAuthToken{ServiceAccountEmail: t.OAuthToken.ServiceAccountEmail, Scope: t.OAuthToken.Scope}
	}

	if t.OidcToken != nil {
		out.OidcToken = &scheddriver.OidcToken{ServiceAccountEmail: t.OidcToken.ServiceAccountEmail, Audience: t.OidcToken.Audience}
	}

	return out
}

func (t *pubsubTargetJSON) toDriver() *scheddriver.PubsubTarget {
	if t == nil {
		return nil
	}

	return &scheddriver.PubsubTarget{TopicName: t.TopicName, Data: t.Data, Attributes: t.Attributes}
}

func (t *appEngineTargetJSON) toDriver() *scheddriver.AppEngineHTTPTarget {
	if t == nil {
		return nil
	}

	out := &scheddriver.AppEngineHTTPTarget{
		HTTPMethod:  defaultMethod(t.HTTPMethod),
		RelativeURI: t.RelativeURI,
		Headers:     t.Headers,
		Body:        t.Body,
	}

	if r := t.AppEngineRouting; r != nil {
		out.AppEngineRouting = &scheddriver.AppEngineRouting{Service: r.Service, Version: r.Version, Instance: r.Instance, Host: r.Host}
	}

	return out
}

func (c *retryConfigJSON) toDriver() *scheddriver.RetryConfig {
	if c == nil {
		return nil
	}

	return &scheddriver.RetryConfig{
		RetryCount:         c.RetryCount,
		MaxRetryDuration:   c.MaxRetryDuration,
		MinBackoffDuration: c.MinBackoffDuration,
		MaxBackoffDuration: c.MaxBackoffDuration,
		MaxDoublings:       c.MaxDoublings,
	}
}

// defaultMethod returns POST for an empty method, matching Cloud Scheduler's
// HTTP_METHOD_UNSPECIFIED default.
func defaultMethod(m string) string {
	if m == "" {
		return methodPost
	}

	return m
}

// toJobJSON renders a driver Job for the wire.
func toJobJSON(j *scheddriver.Job) jobJSON {
	out := jobJSON{
		Name:                j.Name,
		Description:         j.Description,
		Schedule:            j.Schedule,
		TimeZone:            j.TimeZone,
		HTTPTarget:          httpTargetToJSON(j.HTTPTarget),
		PubsubTarget:        pubsubTargetToJSON(j.PubsubTarget),
		AppEngineHTTPTarget: appEngineTargetToJSON(j.AppEngineHTTPTarget),
		RetryConfig:         retryConfigToJSON(j.RetryConfig),
		AttemptDeadline:     j.AttemptDeadline,
		State:               j.State,
		ScheduleTime:        formatTime(j.ScheduleTime),
		LastAttemptTime:     formatTime(j.LastAttemptTime),
		UserUpdateTime:      formatTime(j.UserUpdateTime),
	}

	return out
}

func httpTargetToJSON(t *scheddriver.HTTPTarget) *httpTargetJSON {
	if t == nil {
		return nil
	}

	out := &httpTargetJSON{URI: t.URI, HTTPMethod: t.HTTPMethod, Headers: t.Headers, Body: t.Body}

	if t.OAuthToken != nil {
		out.OAuthToken = &oauthTokenJSON{ServiceAccountEmail: t.OAuthToken.ServiceAccountEmail, Scope: t.OAuthToken.Scope}
	}

	if t.OidcToken != nil {
		out.OidcToken = &oidcTokenJSON{ServiceAccountEmail: t.OidcToken.ServiceAccountEmail, Audience: t.OidcToken.Audience}
	}

	return out
}

func pubsubTargetToJSON(t *scheddriver.PubsubTarget) *pubsubTargetJSON {
	if t == nil {
		return nil
	}

	return &pubsubTargetJSON{TopicName: t.TopicName, Data: t.Data, Attributes: t.Attributes}
}

func appEngineTargetToJSON(t *scheddriver.AppEngineHTTPTarget) *appEngineTargetJSON {
	if t == nil {
		return nil
	}

	out := &appEngineTargetJSON{HTTPMethod: t.HTTPMethod, RelativeURI: t.RelativeURI, Headers: t.Headers, Body: t.Body}

	if r := t.AppEngineRouting; r != nil {
		out.AppEngineRouting = &appEngineRoutingJSON{Service: r.Service, Version: r.Version, Instance: r.Instance, Host: r.Host}
	}

	return out
}

func retryConfigToJSON(c *scheddriver.RetryConfig) *retryConfigJSON {
	if c == nil {
		return nil
	}

	return &retryConfigJSON{
		RetryCount:         c.RetryCount,
		MaxRetryDuration:   c.MaxRetryDuration,
		MinBackoffDuration: c.MinBackoffDuration,
		MaxBackoffDuration: c.MaxBackoffDuration,
		MaxDoublings:       c.MaxDoublings,
	}
}

// formatTime renders a timestamp as RFC3339 (proto3 JSON), or "" when zero so
// the omitempty output-only field is dropped.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
