package config

import "time"

// Options holds configuration for cloudemu services.
type Options struct {
	Clock     Clock
	Region    string
	Latency   time.Duration
	AccountID string
	ProjectID string
}

// Option is a functional option for configuring cloudemu services.
type Option func(*Options)

// NewOptions creates Options from the given functional options.
func NewOptions(opts ...Option) *Options {
	o := &Options{
		Clock:     RealClock{},
		Region:    "us-east-1",
		AccountID: "123456789012",
		ProjectID: "mock-project",
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// WithClock sets the clock implementation.
func WithClock(c Clock) Option {
	return func(o *Options) {
		o.Clock = c
	}
}

// WithRegion sets the cloud region.
func WithRegion(region string) Option {
	return func(o *Options) {
		o.Region = region
	}
}

// WithLatency sets simulated latency for operations.
func WithLatency(d time.Duration) Option {
	return func(o *Options) {
		o.Latency = d
	}
}

// WithAccountID sets the cloud account ID.
func WithAccountID(id string) Option {
	return func(o *Options) {
		o.AccountID = id
	}
}

// WithProjectID sets the cloud project ID.
func WithProjectID(id string) Option {
	return func(o *Options) {
		o.ProjectID = id
	}
}
