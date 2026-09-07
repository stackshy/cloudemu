// Package driver defines the interface and types for the AWS CodeArtifact
// control-plane API (a package registry organized as domains that contain
// repositories). It models domains, repositories, their upstream references and
// external connections, and resource tagging.
//
// The emulator is control-plane only: it does NOT run a package data plane
// (there is no package publish, version resolution or asset storage). A domain
// and a repository are created synchronously in the Active state with stable
// computed fields (the domain arn/owner/createdTime, the repository
// arn/administratorAccount/createdTime) minted once at create and stored, so
// repeated DescribeDomain/DescribeRepository and ListDomains/ListRepositories
// reads never drift. The domain repositoryCount is derived live from the child
// repositories, and assetSizeBytes is always 0 because no assets are stored.
package driver

import (
	"context"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Domain status values. A created domain is Active immediately so IaC waiters do
// not hang on the real-cloud provisioning wait.
const (
	DomainStatusActive  = "Active"
	DomainStatusDeleted = "Deleted"

	// ExternalConnStatusAvailable is the status reported for an associated
	// external connection.
	ExternalConnStatusAvailable = "Available"
)

// Tag is one CodeArtifact resource tag. The API models tags as a list of
// key/value objects, but the emulator stores them as a map internally and
// converts on the wire.
type Tag struct {
	Key   string
	Value string
}

// Domain is a CodeArtifact domain. Arn, Owner, CreatedTime and EncryptionKey are
// computed once at create and stable across reads; RepositoryCount is derived
// live from the child repositories at read time and AssetSizeBytes is always 0.
type Domain struct {
	Name          string
	Owner         string
	Arn           string
	Status        string
	EncryptionKey string
	S3BucketArn   string
	CreatedTime   time.Time
	// AssetSizeBytes is always 0: the emulator stores no package assets.
	AssetSizeBytes int64
	// RepositoryCount is derived live from the child repositories at read time
	// and is not persisted; the stored value is not authoritative.
	RepositoryCount int64
	Tags            map[string]string
}

// UpstreamRef references an upstream repository by name.
type UpstreamRef struct {
	RepositoryName string
}

// ExternalConnection is a repository's association with a public package
// registry (for example public:npmjs).
type ExternalConnection struct {
	ExternalConnectionName string
	PackageFormat          string
	Status                 string
}

// Repository is a CodeArtifact repository. Arn, AdministratorAccount,
// DomainOwner and CreatedTime are computed once at create and stable across
// reads and updates. Upstreams and ExternalConnections are mutated by their own
// operations.
type Repository struct {
	Name                 string
	AdministratorAccount string
	DomainName           string
	DomainOwner          string
	Arn                  string
	Description          string
	CreatedTime          time.Time
	Upstreams            []UpstreamRef
	ExternalConnections  []ExternalConnection
	Tags                 map[string]string
}

// CreateDomainInput is the input to CreateDomain.
type CreateDomainInput struct {
	Name          string
	EncryptionKey string
	Tags          map[string]string
}

// CreateRepositoryInput is the input to CreateRepository.
type CreateRepositoryInput struct {
	Domain      string
	DomainOwner string
	Repository  string
	Description string
	Upstreams   []UpstreamRef
	Tags        map[string]string
}

// UpdateRepositoryInput is the input to UpdateRepository. A nil Description or
// Upstreams means the field was absent from the request and is left unchanged;
// a non-nil pointer (including an empty upstreams list) replaces the value.
type UpdateRepositoryInput struct {
	Domain      string
	DomainOwner string
	Repository  string
	Description *string
	Upstreams   *[]UpstreamRef
}

// CodeArtifact is the CodeArtifact control-plane surface: domains, repositories
// (with upstreams and external connections) and resource tags.
type CodeArtifact interface {
	CreateDomain(ctx context.Context, in *CreateDomainInput) (*Domain, error)
	DescribeDomain(ctx context.Context, domain, domainOwner string) (*Domain, error)
	DeleteDomain(ctx context.Context, domain, domainOwner string) (*Domain, error)
	ListDomains(ctx context.Context, page Page) (domains []*Domain, nextToken string, err error)

	CreateRepository(ctx context.Context, in *CreateRepositoryInput) (*Repository, error)
	DescribeRepository(ctx context.Context, domain, domainOwner, repository string) (*Repository, error)
	UpdateRepository(ctx context.Context, in *UpdateRepositoryInput) (*Repository, error)
	DeleteRepository(ctx context.Context, domain, domainOwner, repository string) (*Repository, error)
	ListRepositories(ctx context.Context, prefix string, page Page) (repos []*Repository, nextToken string, err error)
	ListRepositoriesInDomain(ctx context.Context, domain, domainOwner, prefix string,
		page Page) (repos []*Repository, nextToken string, err error)

	AssociateExternalConnection(ctx context.Context, domain, domainOwner, repository,
		externalConnection string) (*Repository, error)
	DisassociateExternalConnection(ctx context.Context, domain, domainOwner, repository,
		externalConnection string) (*Repository, error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
