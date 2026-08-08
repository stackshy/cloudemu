package config

import "time"

// Defaults applied when a caller sets no corresponding option.
const (
	// DefaultRegion is an AWS region name, so providers with unrelated region
	// naming compare against it to tell "unset" from "chosen".
	DefaultRegion = "us-east-1"

	// DefaultOCIRegion is used when Region is still DefaultRegion.
	DefaultOCIRegion = "us-ashburn-1"

	// DefaultRealm is the OCI realm new OCIDs are minted in.
	DefaultRealm = "oc1"

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
