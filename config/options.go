package config

import (
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Defaults applied when a caller sets no corresponding option.
const (
	// DefaultRegion is an AWS region name, so providers with unrelated region
	// naming compare against it to tell "unset" from "chosen".
	DefaultRegion = "us-east-1"

	// DefaultOCIRegion is used when Region is still DefaultRegion.
	DefaultOCIRegion = "us-ashburn-1"

	// DefaultRealm is the OCI realm new OCIDs are minted in. Aliased from
	// idgen, which owns the OCID format, so the two cannot drift.
	DefaultRealm = idgen.DefaultRealm

	// DefaultTenancyOCID is the built-in tenancy, and the root compartment.
	DefaultTenancyOCID = "ocid1.tenancy.oc1..aaaaaaaacloudemulocaltenancy"
)

// Options holds configuration for cloudemu services.
type Options struct {
	Clock     Clock
	Region    string
	Latency   time.Duration
	AccountID string
	ProjectID string
	// OCI identity. TenancyOCID is the root compartment; CompartmentID is
	// where resources land when a caller names none, and defaults to it.
	TenancyOCID   string
	CompartmentID string
	Realm         string

	// DatabaseEngine optionally backs relational-database instances with a real
	// engine (opt-in). Nil (the default) keeps instances in-memory with
	// synthetic endpoints.
	DatabaseEngine DatabaseEngine

	// CacheEngine optionally backs cache instances with a real cache server
	// (opt-in). Nil (the default) keeps caches in-memory with synthetic
	// endpoints.
	CacheEngine CacheEngine

	// FunctionEngine optionally executes real function code on Invoke (opt-in).
	// Nil (the default) returns a stub payload without running any code.
	FunctionEngine FunctionEngine

	// ComputeEngine optionally backs virtual-machine instances with a real
	// backing (opt-in). Nil (the default) keeps instances in-memory with
	// synthetic state.
	ComputeEngine ComputeEngine

	// ContainerEngine optionally backs container workloads with real containers
	// (opt-in). Nil (the default) keeps workloads in-memory with synthetic state.
	ContainerEngine ContainerEngine
}

// OCIRegion returns the region OCI services should use, substituting an OCI
// region when the caller left Region at the AWS default.
func (o *Options) OCIRegion() string {
	if o.Region == "" || o.Region == DefaultRegion {
		return DefaultOCIRegion
	}

	return o.Region
}

// Option is a functional option for configuring cloudemu services.
type Option func(*Options)

// NewOptions creates Options from the given functional options.
func NewOptions(opts ...Option) *Options {
	o := &Options{
		Clock:       RealClock{},
		Region:      DefaultRegion,
		AccountID:   "123456789012",
		ProjectID:   "mock-project",
		TenancyOCID: DefaultTenancyOCID,
		Realm:       DefaultRealm,
	}
	for _, opt := range opts {
		opt(o)
	}

	// Applied after the options so a custom tenancy becomes the default
	// compartment rather than the built-in root.
	if o.CompartmentID == "" {
		o.CompartmentID = o.TenancyOCID
	}

	return o
}

// WithDatabaseEngine sets the real database engine backing relational-database
// instances. Nil (the default) keeps the emulator in-memory.
func WithDatabaseEngine(e DatabaseEngine) Option {
	return func(o *Options) {
		o.DatabaseEngine = e
	}
}

// WithCacheEngine sets the real cache engine backing cache instances. Nil (the
// default) keeps the emulator in-memory.
func WithCacheEngine(e CacheEngine) Option {
	return func(o *Options) {
		o.CacheEngine = e
	}
}

// WithFunctionEngine sets the real function engine that executes function code
// on Invoke. Nil (the default) returns a stub payload without running code.
func WithFunctionEngine(e FunctionEngine) Option {
	return func(o *Options) {
		o.FunctionEngine = e
	}
}

// WithComputeEngine sets the real compute engine backing virtual-machine
// instances. Nil (the default) keeps the emulator in-memory.
func WithComputeEngine(e ComputeEngine) Option {
	return func(o *Options) {
		o.ComputeEngine = e
	}
}

// WithContainerEngine sets the real container engine backing container
// workloads. Nil (the default) keeps the emulator in-memory.
func WithContainerEngine(e ContainerEngine) Option {
	return func(o *Options) {
		o.ContainerEngine = e
	}
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

// WithTenancyOCID sets the OCI tenancy, which is also the root compartment.
func WithTenancyOCID(id string) Option {
	return func(o *Options) {
		o.TenancyOCID = id
	}
}

// WithCompartmentID sets the OCI compartment new resources default to.
func WithCompartmentID(id string) Option {
	return func(o *Options) {
		o.CompartmentID = id
	}
}

// WithRealm sets the OCI realm embedded in generated OCIDs.
func WithRealm(realm string) Option {
	return func(o *Options) {
		o.Realm = realm
	}
}
