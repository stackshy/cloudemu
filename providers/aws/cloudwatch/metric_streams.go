package cloudwatch

// This file implements CloudWatch metric-stream operations (PutMetricStream,
// GetMetricStream, ListMetricStreams, DeleteMetricStream, StartMetricStreams,
// StopMetricStreams, and their tags), backing the aws_cloudwatch_metric_stream
// Terraform resource. The store is an AWS-local optional capability so the
// shared Monitoring interface — and the Azure/GCP providers — stay unchanged.

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// validMetricStreamOutputFormats is the closed OutputFormat enum documented by
// PutMetricStream. A value outside this set is rejected, matching AWS, rather
// than silently stored.
//
//nolint:gochecknoglobals // fixed lookup table for a closed enum.
var validMetricStreamOutputFormats = map[string]bool{
	"json":             true,
	"opentelemetry1.0": true,
	"opentelemetry0.7": true,
}

const (
	metricStreamStateRunning = "running"
	metricStreamStateStopped = "stopped"
)

// storedMetricStream is a persisted CloudWatch metric stream.
type storedMetricStream struct {
	Name                         string
	ARN                          string
	FirehoseARN                  string
	RoleARN                      string
	OutputFormat                 string
	State                        string
	IncludeFilters               []driver.MetricStreamFilter
	ExcludeFilters               []driver.MetricStreamFilter
	StatisticsConfigurations     []driver.MetricStreamStatisticsConfig
	IncludeLinkedAccountsMetrics bool
	Tags                         map[string]string
	CreationDate                 time.Time
	LastUpdateDate               time.Time
}

// PutMetricStream creates or updates a metric stream. Creating a new stream
// starts it in the "running" state (real CloudWatch semantics); updating an
// existing one leaves its State unchanged. Tags are applied only when the
// stream is being created — an update's Tags are ignored, matching the real
// PutMetricStream API (use TagResource/UntagResource to retag an existing
// stream). It returns the stream's ARN.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) PutMetricStream(_ context.Context, cfg driver.MetricStreamConfig) (string, error) {
	if err := validateMetricStreamConfig(&cfg); err != nil {
		return "", err
	}

	arn := idgen.AWSARN("cloudwatch", m.opts.Region, m.opts.AccountID, "metric-stream/"+cfg.Name)
	now := m.opts.Clock.Now()

	state := metricStreamStateRunning
	created := now
	tags := copyDims(cfg.Tags)

	if existing, ok := m.metricStreams.Get(cfg.Name); ok {
		state = existing.State
		created = existing.CreationDate
		tags = existing.Tags
	}

	stream := &storedMetricStream{
		Name:                         cfg.Name,
		ARN:                          arn,
		FirehoseARN:                  cfg.FirehoseARN,
		RoleARN:                      cfg.RoleARN,
		OutputFormat:                 cfg.OutputFormat,
		State:                        state,
		IncludeFilters:               append([]driver.MetricStreamFilter{}, cfg.IncludeFilters...),
		ExcludeFilters:               append([]driver.MetricStreamFilter{}, cfg.ExcludeFilters...),
		StatisticsConfigurations:     append([]driver.MetricStreamStatisticsConfig{}, cfg.StatisticsConfigurations...),
		IncludeLinkedAccountsMetrics: cfg.IncludeLinkedAccountsMetrics,
		Tags:                         tags,
		CreationDate:                 created,
		LastUpdateDate:               now,
	}

	m.metricStreams.Set(cfg.Name, stream)

	return arn, nil
}

// validateMetricStreamConfig checks the required fields and closed enums that
// real PutMetricStream validates server-side.
func validateMetricStreamConfig(cfg *driver.MetricStreamConfig) error {
	if cfg.Name == "" {
		return errors.Newf(errors.InvalidArgument, "metric stream name is required")
	}

	if cfg.FirehoseARN == "" {
		return errors.Newf(errors.InvalidArgument, "FirehoseArn is required")
	}

	if cfg.RoleARN == "" {
		return errors.Newf(errors.InvalidArgument, "RoleArn is required")
	}

	if !validMetricStreamOutputFormats[cfg.OutputFormat] {
		return errors.Newf(errors.InvalidArgument, "invalid OutputFormat %q", cfg.OutputFormat)
	}

	if len(cfg.IncludeFilters) > 0 && len(cfg.ExcludeFilters) > 0 {
		return errors.Newf(errors.InvalidArgument, "cannot include IncludeFilters and ExcludeFilters in the same operation")
	}

	return nil
}

// GetMetricStream returns the named metric stream, or NotFound (the
// CloudWatch ResourceNotFoundException) when it does not exist.
func (m *Mock) GetMetricStream(_ context.Context, name string) (*driver.MetricStreamInfo, error) {
	s, ok := m.metricStreams.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "metric stream %q not found", name)
	}

	return toMetricStreamInfo(s), nil
}

// ListMetricStreams returns every stored metric stream as a summary entry,
// sorted by name.
func (m *Mock) ListMetricStreams(_ context.Context) ([]driver.MetricStreamEntry, error) {
	all := m.metricStreams.All()
	out := make([]driver.MetricStreamEntry, 0, len(all))

	for _, s := range all {
		out = append(out, driver.MetricStreamEntry{
			Name:           s.Name,
			ARN:            s.ARN,
			FirehoseARN:    s.FirehoseARN,
			OutputFormat:   s.OutputFormat,
			State:          s.State,
			CreationDate:   s.CreationDate,
			LastUpdateDate: s.LastUpdateDate,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// DeleteMetricStream permanently deletes the named metric stream. Like real
// CloudWatch (whose DeleteMetricStream documents no not-found error), deleting
// an unknown name is a no-op rather than an error.
func (m *Mock) DeleteMetricStream(_ context.Context, name string) error {
	m.metricStreams.Delete(name)

	return nil
}

// StartMetricStreams sets the named streams to the "running" state. Unknown
// names are silently skipped, matching real StartMetricStreams (which
// documents no not-found error).
func (m *Mock) StartMetricStreams(ctx context.Context, names []string) error {
	return m.setMetricStreamState(ctx, names, metricStreamStateRunning)
}

// StopMetricStreams sets the named streams to the "stopped" state. Unknown
// names are silently skipped, matching real StopMetricStreams.
func (m *Mock) StopMetricStreams(ctx context.Context, names []string) error {
	return m.setMetricStreamState(ctx, names, metricStreamStateStopped)
}

func (m *Mock) setMetricStreamState(_ context.Context, names []string, state string) error {
	now := m.opts.Clock.Now()

	for _, name := range names {
		m.metricStreams.Update(name, func(s *storedMetricStream) *storedMetricStream {
			cp := *s
			cp.State = state
			cp.LastUpdateDate = now

			return &cp
		})
	}

	return nil
}

// AddMetricStreamTags merges tags onto the named metric stream, backing
// TagResource for a metric-stream ARN.
func (m *Mock) AddMetricStreamTags(_ context.Context, name string, tags map[string]string) error {
	ok := m.metricStreams.Update(name, func(s *storedMetricStream) *storedMetricStream {
		cp := *s
		cp.Tags = make(map[string]string, len(s.Tags)+len(tags))

		for k, v := range s.Tags {
			cp.Tags[k] = v
		}

		for k, v := range tags {
			cp.Tags[k] = v
		}

		return &cp
	})
	if !ok {
		return errors.Newf(errors.NotFound, "metric stream %q not found", name)
	}

	return nil
}

// RemoveMetricStreamTags deletes the given tag keys from the named metric
// stream, backing UntagResource for a metric-stream ARN.
func (m *Mock) RemoveMetricStreamTags(_ context.Context, name string, keys []string) error {
	ok := m.metricStreams.Update(name, func(s *storedMetricStream) *storedMetricStream {
		cp := *s
		cp.Tags = make(map[string]string, len(s.Tags))

		for k, v := range s.Tags {
			cp.Tags[k] = v
		}

		for _, k := range keys {
			delete(cp.Tags, k)
		}

		return &cp
	})
	if !ok {
		return errors.Newf(errors.NotFound, "metric stream %q not found", name)
	}

	return nil
}

// MetricStreamTags returns a copy of the named metric stream's tags, backing
// ListTagsForResource for a metric-stream ARN.
func (m *Mock) MetricStreamTags(_ context.Context, name string) (map[string]string, error) {
	s, ok := m.metricStreams.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "metric stream %q not found", name)
	}

	return copyDims(s.Tags), nil
}

func toMetricStreamInfo(s *storedMetricStream) *driver.MetricStreamInfo {
	return &driver.MetricStreamInfo{
		Name:                         s.Name,
		ARN:                          s.ARN,
		FirehoseARN:                  s.FirehoseARN,
		RoleARN:                      s.RoleARN,
		OutputFormat:                 s.OutputFormat,
		State:                        s.State,
		IncludeFilters:               append([]driver.MetricStreamFilter{}, s.IncludeFilters...),
		ExcludeFilters:               append([]driver.MetricStreamFilter{}, s.ExcludeFilters...),
		StatisticsConfigurations:     append([]driver.MetricStreamStatisticsConfig{}, s.StatisticsConfigurations...),
		IncludeLinkedAccountsMetrics: s.IncludeLinkedAccountsMetrics,
		CreationDate:                 s.CreationDate,
		LastUpdateDate:               s.LastUpdateDate,
	}
}
