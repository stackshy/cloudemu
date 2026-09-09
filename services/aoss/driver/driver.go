// Package driver defines the interface and types for the Amazon OpenSearch
// Serverless (aoss) control-plane API (AWS JSON 1.0, X-Amz-Target prefix
// "OpenSearchServerless."). It models serverless collections plus the
// encryption/network security policies and data access policies that govern
// them, and resource tags.
//
// This is a control-plane-only surface: the emulator never runs a search
// engine. A collection is created directly in the ACTIVE state so an IaC waiter
// that blocks on status (Terraform's aws_opensearchserverless_collection polls
// BatchGetCollection for ACTIVE) does not hang. The computed fields clients and
// IaC tools read back — the collection id ([a-z0-9]{20}), the derived arn, the
// collectionEndpoint/dashboardEndpoint, the status and the createdDate — are
// minted once at create and stored, so repeated BatchGetCollection/
// ListCollections reads and a later UpdateCollection never drift. A policy's
// policyVersion is minted at create and re-minted on update (monotonic in the
// sense that an update always yields a new opaque token); the policy document
// round-trips as a parsed JSON value.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Collection status values. A newly created collection is ACTIVE so an IaC
// create waiter completes without a multi-minute real-cloud provisioning wait.
const (
	StatusCreating = "CREATING"
	StatusActive   = "ACTIVE"
	StatusDeleting = "DELETING"
	StatusFailed   = "FAILED"
)

// Collection type values.
const (
	CollectionTypeSearch       = "SEARCH"
	CollectionTypeTimeSeries   = "TIMESERIES"
	CollectionTypeVectorSearch = "VECTORSEARCH"
)

// Security policy type values.
const (
	SecurityPolicyEncryption = "encryption"
	SecurityPolicyNetwork    = "network"
)

// AccessPolicyData is the only valid access policy type.
const AccessPolicyData = "data"

// Tag is a collection tag (key/value pair). OpenSearch Serverless models tags
// as an array of {key,value} objects rather than a map.
type Tag struct {
	Key   string
	Value string
}

// Collection is an OpenSearch Serverless collection. ID, ARN, CollectionEndpoint,
// DashboardEndpoint, Status and CreatedDate are computed once at create and never
// regenerated, so repeated reads never drift; LastModifiedDate is bumped on every
// mutation. KmsKeyArn is resolved from the matching encryption security policy at
// create.
type Collection struct {
	ID                 string
	ARN                string
	Name               string
	Description        string
	Type               string
	Status             string
	StandbyReplicas    string
	KmsKeyArn          string
	CreatedDate        time.Time
	LastModifiedDate   time.Time
	CollectionEndpoint string
	DashboardEndpoint  string
	Tags               []Tag
}

// CollectionError is a per-id/name failure entry returned by BatchGetCollection
// for a requested collection that does not exist.
type CollectionError struct {
	ID           string
	Name         string
	ErrorCode    string
	ErrorMessage string
}

// Policy is a security (encryption/network) or data access policy. Type, Name,
// CreatedDate are set at create; Policy carries the parsed JSON document;
// PolicyVersion is a fresh opaque token at create and on every update.
type Policy struct {
	Type             string
	Name             string
	Description      string
	Policy           json.RawMessage
	PolicyVersion    string
	CreatedDate      time.Time
	LastModifiedDate time.Time
}

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// CreateCollectionInput is the input to CreateCollection.
type CreateCollectionInput struct {
	Name            string
	Description     string
	Type            string
	StandbyReplicas string
	Tags            []Tag
}

// UpdateCollectionInput is the input to UpdateCollection. A nil pointer means the
// parameter was omitted and the stored value is left unchanged.
type UpdateCollectionInput struct {
	ID          string
	Description *string
}

// CreatePolicyInput is the input to CreateSecurityPolicy/CreateAccessPolicy. The
// server has already validated Policy is a JSON document and passes it parsed.
type CreatePolicyInput struct {
	Type        string
	Name        string
	Description string
	Policy      json.RawMessage
}

// UpdatePolicyInput is the input to UpdateSecurityPolicy/UpdateAccessPolicy. A
// nil Description leaves it unchanged; a nil Policy leaves the document unchanged.
// PolicyVersion is the caller-supplied current version for optimistic concurrency.
type UpdatePolicyInput struct {
	Type          string
	Name          string
	Description   *string
	Policy        json.RawMessage
	PolicyVersion string
}

// AOSS is the Amazon OpenSearch Serverless control-plane surface: collections,
// security policies (encryption/network), data access policies, and tags.
type AOSS interface {
	CreateCollection(ctx context.Context, in *CreateCollectionInput) (*Collection, error)
	BatchGetCollection(ctx context.Context, ids, names []string) (details []Collection, errs []CollectionError, err error)
	ListCollections(ctx context.Context, filterName, filterStatus string, page Page) (cols []Collection, nextToken string, err error)
	UpdateCollection(ctx context.Context, in *UpdateCollectionInput) (*Collection, error)
	DeleteCollection(ctx context.Context, id string) (*Collection, error)

	CreateSecurityPolicy(ctx context.Context, in *CreatePolicyInput) (*Policy, error)
	GetSecurityPolicy(ctx context.Context, policyType, name string) (*Policy, error)
	ListSecurityPolicies(ctx context.Context, policyType string, page Page) (policies []Policy, nextToken string, err error)
	UpdateSecurityPolicy(ctx context.Context, in *UpdatePolicyInput) (*Policy, error)
	DeleteSecurityPolicy(ctx context.Context, policyType, name string) error

	CreateAccessPolicy(ctx context.Context, in *CreatePolicyInput) (*Policy, error)
	GetAccessPolicy(ctx context.Context, policyType, name string) (*Policy, error)
	ListAccessPolicies(ctx context.Context, policyType string, page Page) (policies []Policy, nextToken string, err error)
	UpdateAccessPolicy(ctx context.Context, in *UpdatePolicyInput) (*Policy, error)
	DeleteAccessPolicy(ctx context.Context, policyType, name string) error

	TagResource(ctx context.Context, resourceArn string, tags []Tag) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) ([]Tag, error)
}
