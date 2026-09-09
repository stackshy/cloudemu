// Package driver defines the interface and types for the AWS HealthLake
// control-plane API (AWS JSON 1.0, X-Amz-Target prefix "HealthLake."). It
// models FHIR data stores and their resource tags.
//
// This is a control-plane-only surface: the emulator never stores or serves
// FHIR resources and never runs import/export jobs (those are the data plane
// and are out of scope). A data store is created directly in a stable, ACTIVE
// shape so an IaC apply completes without a provisioning wait.
//
// The computed fields clients and IaC read back are minted once at create and
// stored, so repeated Describe/List reads never drift: a data store's
// DatastoreID, DatastoreArn
// (arn:aws:healthlake:<region>:<acct>:datastore/fhir/<id>), DatastoreEndpoint
// (https://healthlake.<region>.amazonaws.com/datastore/<id>/r4/), its
// DatastoreStatus (ACTIVE) and CreatedAt. The SSE, preload and identity-provider
// configuration blocks round-trip verbatim so a nested IaC block cannot drift a
// plan through a lossy re-marshal.
package driver

import (
	"context"
	"time"
)

// The data-store lifecycle statuses HealthLake reports. The emulator activates a
// data store synchronously, so a created store is ACTIVE at once and a delete
// removes it (the delete response reports DELETED); a discovering IaC waiter
// therefore completes without a provisioning wait.
const (
	StatusCreating = "CREATING"
	StatusActive   = "ACTIVE"
	StatusDeleting = "DELETING"
	StatusDeleted  = "DELETED"
)

// FHIRVersionR4 is the only FHIR release version HealthLake supports.
const FHIRVersionR4 = "R4"

// The customer-managed-key types a data store's KMS encryption config reports.
// A data store created without an explicit SSE config is encrypted with an
// AWS-owned key, matching real HealthLake.
const (
	CmkTypeAWSOwned        = "AWS_OWNED_KMS_KEY"
	CmkTypeCustomerManaged = "CUSTOMER_MANAGED_KMS_KEY"
)

// Tag is a resource tag (key/value pair). HealthLake models tags as an array of
// {Key,Value} objects rather than a map.
type Tag struct {
	Key   string
	Value string
}

// KmsEncryptionConfig is the KMS encryption configuration of a data store. It
// round-trips verbatim. CmkType is one of AWS_OWNED_KMS_KEY or
// CUSTOMER_MANAGED_KMS_KEY; KmsKeyID is set only for a customer-managed key.
type KmsEncryptionConfig struct {
	CmkType  string
	KmsKeyID string
}

// SseConfiguration is the server-side encryption configuration of a data store.
type SseConfiguration struct {
	KmsEncryptionConfig *KmsEncryptionConfig
}

// PreloadDataConfig requests that a data store be preloaded with open-source
// Synthea FHIR data. PreloadDataType is always SYNTHEA. It round-trips verbatim.
type PreloadDataConfig struct {
	PreloadDataType string
}

// IdentityProviderConfiguration is the identity-provider configuration selected
// when a data store is created. It round-trips verbatim.
type IdentityProviderConfiguration struct {
	AuthorizationStrategy           string
	FineGrainedAuthorizationEnabled bool
	IdpLambdaArn                    string
	Metadata                        string
}

// Datastore is a HealthLake FHIR data store. DatastoreID, DatastoreArn,
// DatastoreEndpoint, DatastoreStatus and CreatedAt are computed once at create
// and never regenerated, so repeated reads never drift. The SSE, preload and
// identity-provider configuration blocks round-trip verbatim.
type Datastore struct {
	DatastoreID                   string
	DatastoreArn                  string
	DatastoreEndpoint             string
	DatastoreName                 string
	DatastoreStatus               string
	DatastoreTypeVersion          string
	CreatedAt                     time.Time
	SseConfiguration              *SseConfiguration
	PreloadDataConfig             *PreloadDataConfig
	IdentityProviderConfiguration *IdentityProviderConfiguration
	Tags                          []Tag
}

// CreateFHIRDatastoreInput is the input to CreateFHIRDatastore.
type CreateFHIRDatastoreInput struct {
	DatastoreName                 string
	DatastoreTypeVersion          string
	SseConfiguration              *SseConfiguration
	PreloadDataConfig             *PreloadDataConfig
	IdentityProviderConfiguration *IdentityProviderConfiguration
	Tags                          []Tag
}

// ListFilter narrows a ListFHIRDatastores query. A zero value matches every
// data store.
type ListFilter struct {
	DatastoreName   string
	DatastoreStatus string
}

// Page is the pagination cursor shared by the list operation.
type Page struct {
	NextToken  string
	MaxResults int32
}

// HealthLake is the AWS HealthLake control-plane surface: FHIR data stores and
// their resource tags.
type HealthLake interface {
	CreateFHIRDatastore(ctx context.Context, in *CreateFHIRDatastoreInput) (*Datastore, error)
	DescribeFHIRDatastore(ctx context.Context, datastoreID string) (*Datastore, error)
	DeleteFHIRDatastore(ctx context.Context, datastoreID string) (*Datastore, error)
	ListFHIRDatastores(ctx context.Context, filter ListFilter, page Page) (datastores []Datastore, nextToken string, err error)

	TagResource(ctx context.Context, resourceARN string, tags []Tag) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) ([]Tag, error)
}
