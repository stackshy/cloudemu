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

	// StorageEngine optionally persists object-storage bytes to a real backing
	// (opt-in). Nil (the default) keeps object bytes in-memory.
	StorageEngine StorageEngine

	// AsyncSettle, when true, makes resources report a realistic intermediate
	// state (pending/creating/PENDING_VALIDATION/RUNNING) for a short settle
	// window after creation before their final state. Default false, which keeps
	// every resource reporting its terminal state immediately (the historical
	// behavior). See internal/settle.
	AsyncSettle bool

	// EnforceAuth, when true, makes the AWS wire server verify the SigV4
	// signature on each incoming request against a registered IAM access key and
	// reject bad/missing signatures with 403. It also gates JSON-RPC operations
	// through IAM authorization (see WithEnforceAuth for the exact scope and
	// limitations). Default false, which accepts any credentials exactly as
	// before (the historical behavior).
	EnforceAuth bool
}

// SettleDuration returns d when asynchronous state settling is enabled and 0
// when it is not, so a provider can write `settle.Pending(state, now,
// o.SettleDuration(settle.DefaultInstanceSettle))` at one call site and have the
// window be inactive (immediate final state) unless the caller opted in.
func (o *Options) SettleDuration(d time.Duration) time.Duration {
	if o.AsyncSettle {
		return d
	}

	return 0
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

// WithStorageEngine sets the real storage engine that persists object-storage
// bytes (opt-in). Nil (the default) keeps object bytes in-memory.
func WithStorageEngine(e StorageEngine) Option {
	return func(o *Options) {
		o.StorageEngine = e
	}
}

// WithClock sets the clock implementation.
func WithClock(c Clock) Option {
	return func(o *Options) {
		o.Clock = c
	}
}

// WithAsyncSettle enables realistic post-creation state settling (resources
// report an intermediate state for a short window before their final state).
// Off by default. Pair with a FakeClock to drive the transition deterministically
// in tests. See internal/settle.
func WithAsyncSettle() Option {
	return func(o *Options) {
		o.AsyncSettle = true
	}
}

// WithEnforceAuth turns on AWS SigV4 request authentication and IAM
// authorization: the wire server verifies each request's signature against a
// registered IAM access key (rejecting bad/missing signatures with 403) and then
// checks the caller's IAM policies before dispatch. Off by default, which accepts
// any credentials (the historical behavior).
//
// Authentication scope: long-term (AKIA) access-key signatures are verified; STS
// temporary (ASIA) credentials are accepted without signature verification (a
// follow-up), and request timestamps are not checked for expiry.
//
// Authorization scope: enforced for JSON-RPC services (DynamoDB, KMS, SQS, …),
// where the operation is bound to the X-Amz-Target header the dispatcher routes
// on. Query and REST services are authenticated only — their executed operation's
// IAM service cannot be soundly determined before dispatch (query routing is by
// action name and the SigV4 credential scope is client-controlled), so
// action+resource authorization for them is a follow-up. Authorization is
// action-level (resource "*").
//
// Foundation limitation: authorization applies only to principals that have IAM
// policies defined. A valid IAM user or role with NO policies is left
// unrestricted here (real AWS implicit-denies a policy-less principal), and the
// account-admin/root and ASIA bootstrap identities are always allowed.
func WithEnforceAuth() Option {
	return func(o *Options) {
		o.EnforceAuth = true
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
