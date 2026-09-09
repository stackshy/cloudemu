// Package driver defines the interface and types for the Amazon CloudFront
// distribution control plane.
//
// CloudFront is an AWS-only REST/XML service (API version 2020-05-31). The
// emulator models the distribution control plane: create/get/update/delete/list
// of distributions, their ETag-based optimistic concurrency, synchronous
// Deployed status, tags, and a synchronous invalidation surface. It does NOT
// emulate any edge/CDN data plane.
//
// A distribution's DistributionConfig is a large, open-ended XML sub-tree that
// must round-trip byte-for-byte (Terraform diffs it deeply on every plan, so a
// single dropped field is perpetual drift). Rather than model every nested
// field as a Go type, the config is carried verbatim as ConfigXML — the inner
// XML of the <DistributionConfig> element — exactly as GuardDuty carries
// open-ended feature blocks as raw JSON. The handful of scalars the emulator
// must interpret (CallerReference for dedup, Enabled for the delete-guard,
// Comment) are lifted out alongside it.
package driver

import (
	"context"
	"errors"
	"time"
)

// Status values a distribution reports. The emulator has no asynchronous
// propagation, so a distribution is Deployed the moment it is created or
// updated — this is deliberate: aws_cloudfront_distribution blocks on a
// Status=Deployed waiter, and a distribution stuck InProgress would hang the
// apply forever.
const (
	StatusDeployed   = "Deployed"
	StatusInProgress = "InProgress"
)

// InvalidationStatus values. Invalidations complete synchronously.
const (
	InvalidationCompleted  = "Completed"
	InvalidationInProgress = "InProgress"
)

// Sentinel errors mapped by the wire handler to CloudFront's XML error codes
// and HTTP statuses. They are distinct because CloudFront distinguishes an
// If-Match ETag mismatch (PreconditionFailed, HTTP 412) from a delete of an
// enabled distribution (DistributionNotDisabled, HTTP 409) from a
// missing/blank If-Match header (InvalidIfMatchVersion, HTTP 400) — outcomes a
// single canonical error code cannot separate.
var (
	// ErrNoSuchDistribution — the distribution id does not exist (HTTP 404).
	ErrNoSuchDistribution = errors.New("the specified distribution does not exist")
	// ErrDistributionAlreadyExists — the CallerReference was already used by an
	// existing distribution (HTTP 409).
	ErrDistributionAlreadyExists = errors.New("the caller reference is associated with a distribution that already exists")
	// ErrInvalidIfMatchVersion — the If-Match header is missing or blank (HTTP 400).
	ErrInvalidIfMatchVersion = errors.New("the If-Match version is missing or not valid")
	// ErrPreconditionFailed — the If-Match ETag does not match the current one (HTTP 412).
	ErrPreconditionFailed = errors.New("the precondition in one or more of the request-header fields evaluated to false")
	// ErrDistributionNotDisabled — the distribution must be disabled (Enabled=false)
	// before it can be deleted (HTTP 409).
	ErrDistributionNotDisabled = errors.New("the distribution you are trying to delete has not been disabled")
	// ErrNoSuchInvalidation — the invalidation id does not exist (HTTP 404).
	ErrNoSuchInvalidation = errors.New("the specified invalidation does not exist")
	// ErrCallerReferenceImmutable — an update changed the CallerReference, which
	// is fixed for the life of a distribution (HTTP 400, IllegalUpdate).
	ErrCallerReferenceImmutable = errors.New("the update contains modifications that are not allowed for the given caller reference")
)

// Distribution is a CloudFront distribution: its server-assigned identity and
// status plus the verbatim DistributionConfig it was created/updated with.
type Distribution struct {
	// ID is the 14-character distribution id ("E" + 13 uppercase alphanumerics).
	ID string
	// ARN is arn:aws:cloudfront::<account>:distribution/<ID> (region-less).
	ARN string
	// Status is StatusDeployed once created (the emulator never leaves InProgress).
	Status string
	// DomainName is the distribution's <random>.cloudfront.net hostname.
	DomainName string
	// ETag is the opaque optimistic-concurrency token, rotated on every update.
	ETag string
	// LastModifiedTime is when the config was last created or updated.
	LastModifiedTime time.Time
	// CallerReference is the caller's idempotency token, lifted from the config;
	// it dedups creates and is immutable across updates.
	CallerReference string
	// Enabled is lifted from the config; a distribution must be disabled before
	// it can be deleted.
	Enabled bool
	// Comment is lifted from the config (used only for summaries).
	Comment string
	// ConfigXML is the verbatim inner XML of the <DistributionConfig> element,
	// stored exactly as received so a read round-trips every field byte-for-byte.
	ConfigXML []byte
	// Tags are the distribution's resource tags.
	Tags map[string]string
	// seq orders ListDistributions deterministically by creation.
	Seq int64
}

// CreateDistributionInput carries a new distribution's config and tags.
type CreateDistributionInput struct {
	CallerReference string
	Enabled         bool
	Comment         string
	ConfigXML       []byte
	Tags            map[string]string
}

// UpdateDistributionInput carries a replacement config guarded by an ETag.
type UpdateDistributionInput struct {
	ID              string
	IfMatch         string
	CallerReference string
	Enabled         bool
	Comment         string
	ConfigXML       []byte
}

// Invalidation is a synchronous cache-invalidation request against a distribution.
type Invalidation struct {
	ID              string
	Status          string
	CreateTime      time.Time
	CallerReference string
	Paths           []string
}

// CreateInvalidationInput carries the invalidation batch.
type CreateInvalidationInput struct {
	CallerReference string
	Paths           []string
}

// CloudFront is the interface a CloudFront backend implements. It carries the
// distribution control plane, a synchronous invalidation surface, and the
// resource-tagging operations Terraform reads on every refresh.
type CloudFront interface {
	// Distributions.
	CreateDistribution(ctx context.Context, in *CreateDistributionInput) (*Distribution, error)
	GetDistribution(ctx context.Context, id string) (*Distribution, error)
	UpdateDistribution(ctx context.Context, in *UpdateDistributionInput) (*Distribution, error)
	DeleteDistribution(ctx context.Context, id, ifMatch string) error
	ListDistributions(ctx context.Context) ([]Distribution, error)

	// Invalidations (synchronous — every invalidation is Completed immediately).
	CreateInvalidation(ctx context.Context, distributionID string, in *CreateInvalidationInput) (*Invalidation, error)
	GetInvalidation(ctx context.Context, distributionID, invalidationID string) (*Invalidation, error)
	ListInvalidations(ctx context.Context, distributionID string) ([]Invalidation, error)

	// Tags (ARN-keyed).
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
}
