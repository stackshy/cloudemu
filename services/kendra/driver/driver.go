// Package driver defines the interface and types for the Amazon Kendra
// control-plane API (AWS JSON 1.1, X-Amz-Target prefix
// "AWSKendraFrontendService."). It models Kendra indexes and the data source
// connectors that belong to them, plus resource tags.
//
// This is a control-plane-only surface: the emulator never runs a search
// engine and never indexes documents. An index and a data source are created
// directly in the ACTIVE state so an IaC waiter that blocks on status
// (Terraform's aws_kendra_index / aws_kendra_data_source poll DescribeIndex /
// DescribeDataSource for ACTIVE) does not hang — real Kendra index creation
// takes ~30 minutes, so returning ACTIVE synchronously is what keeps the
// emulator usable. The computed fields clients and IaC read back — the index id
// (a 36-character UUID), the data source id, the status and the createdAt/
// updatedAt timestamps — are minted once at create and stored, so repeated
// Describe/List reads and a later Update never drift. Kendra's API does not
// return an ARN; Terraform derives it from the id, so the id's stability is
// what keeps the arn attribute drift-free.
//
// Rich nested configuration blocks (the index server-side encryption, capacity
// units, document-metadata and user-context configuration; the data source
// Configuration, VpcConfiguration and CustomDocumentEnrichmentConfiguration)
// round-trip verbatim as raw JSON, so a deeply nested block cannot drift an IaC
// plan through a lossy re-marshal.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Index status values. A newly created index is ACTIVE so an IaC create waiter
// completes without the multi-minute real-cloud provisioning wait.
const (
	IndexStatusCreating       = "CREATING"
	IndexStatusActive         = "ACTIVE"
	IndexStatusDeleting       = "DELETING"
	IndexStatusFailed         = "FAILED"
	IndexStatusUpdating       = "UPDATING"
	IndexStatusSystemUpdating = "SYSTEM_UPDATING"
)

// Index edition values. ENTERPRISE_EDITION is the real-cloud default when the
// caller omits one.
const (
	EditionDeveloper       = "DEVELOPER_EDITION"
	EditionEnterprise      = "ENTERPRISE_EDITION"
	EditionGenAIEnterprise = "GEN_AI_ENTERPRISE_EDITION"
)

// UserContextPolicy values. ATTRIBUTE_FILTER is the value reported when the
// caller omits one, matching the real API's default.
const (
	UserContextAttributeFilter = "ATTRIBUTE_FILTER"
	UserContextUserToken       = "USER_TOKEN"
)

// DataSource status values. A newly created data source is ACTIVE.
const (
	DataSourceStatusCreating = "CREATING"
	DataSourceStatusActive   = "ACTIVE"
	DataSourceStatusDeleting = "DELETING"
	DataSourceStatusFailed   = "FAILED"
	DataSourceStatusUpdating = "UPDATING"
)

// DataSourceTypeCustom is the CUSTOM connector type, the only type that must
// not carry a Configuration/RoleArn/Schedule.
const DataSourceTypeCustom = "CUSTOM"

// Tag is a resource tag (key/value pair). Kendra models tags as an array of
// {Key,Value} objects rather than a map.
type Tag struct {
	Key   string
	Value string
}

// Index is a Kendra index. ID, Status, CreatedAt and Edition are computed once
// at create and never regenerated, so repeated reads never drift; UpdatedAt is
// bumped on every mutation. The nested configuration blocks are stored verbatim
// as raw JSON so they round-trip without re-marshal drift.
type Index struct {
	ID                                string
	Name                              string
	Edition                           string
	RoleArn                           string
	Description                       string
	Status                            string
	UserContextPolicy                 string
	ServerSideEncryptionConfiguration json.RawMessage
	CapacityUnits                     json.RawMessage
	DocumentMetadataConfigurations    json.RawMessage
	UserGroupResolutionConfiguration  json.RawMessage
	UserTokenConfigurations           json.RawMessage
	CreatedAt                         time.Time
	UpdatedAt                         time.Time
	Tags                              []Tag
}

// DataSource is a Kendra data source connector that belongs to an index. ID,
// IndexId, Type, Status and CreatedAt are computed once at create; UpdatedAt is
// bumped on every mutation. The rich Configuration and enrichment blocks round-
// trip verbatim as raw JSON.
type DataSource struct {
	ID                                    string
	IndexID                               string
	Name                                  string
	Type                                  string
	RoleArn                               string
	Description                           string
	Schedule                              string
	LanguageCode                          string
	Status                                string
	Configuration                         json.RawMessage
	VpcConfiguration                      json.RawMessage
	CustomDocumentEnrichmentConfiguration json.RawMessage
	CreatedAt                             time.Time
	UpdatedAt                             time.Time
	Tags                                  []Tag
}

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// CreateIndexInput is the input to CreateIndex.
type CreateIndexInput struct {
	Name                              string
	Edition                           string
	RoleArn                           string
	Description                       string
	UserContextPolicy                 string
	ServerSideEncryptionConfiguration json.RawMessage
	UserGroupResolutionConfiguration  json.RawMessage
	UserTokenConfigurations           json.RawMessage
	Tags                              []Tag
}

// UpdateIndexInput is the input to UpdateIndex. A nil pointer/raw means the
// parameter was omitted and the stored value is left unchanged.
type UpdateIndexInput struct {
	ID                                   string
	Name                                 *string
	RoleArn                              *string
	Description                          *string
	UserContextPolicy                    *string
	CapacityUnits                        json.RawMessage
	DocumentMetadataConfigurationUpdates json.RawMessage
	UserGroupResolutionConfiguration     json.RawMessage
	UserTokenConfigurations              json.RawMessage
}

// CreateDataSourceInput is the input to CreateDataSource.
type CreateDataSourceInput struct {
	IndexID                               string
	Name                                  string
	Type                                  string
	RoleArn                               string
	Description                           string
	Schedule                              string
	LanguageCode                          string
	Configuration                         json.RawMessage
	VpcConfiguration                      json.RawMessage
	CustomDocumentEnrichmentConfiguration json.RawMessage
	Tags                                  []Tag
}

// UpdateDataSourceInput is the input to UpdateDataSource. A nil pointer/raw
// leaves the stored value unchanged.
type UpdateDataSourceInput struct {
	ID                                    string
	IndexID                               string
	Name                                  *string
	RoleArn                               *string
	Description                           *string
	Schedule                              *string
	LanguageCode                          *string
	Configuration                         json.RawMessage
	VpcConfiguration                      json.RawMessage
	CustomDocumentEnrichmentConfiguration json.RawMessage
}

// Kendra is the Amazon Kendra control-plane surface: indexes, the data source
// connectors that belong to them, and resource tags.
type Kendra interface {
	CreateIndex(ctx context.Context, in *CreateIndexInput) (*Index, error)
	DescribeIndex(ctx context.Context, id string) (*Index, error)
	UpdateIndex(ctx context.Context, in *UpdateIndexInput) error
	DeleteIndex(ctx context.Context, id string) error
	ListIndices(ctx context.Context, page Page) (indexes []Index, nextToken string, err error)

	CreateDataSource(ctx context.Context, in *CreateDataSourceInput) (*DataSource, error)
	DescribeDataSource(ctx context.Context, indexID, id string) (*DataSource, error)
	UpdateDataSource(ctx context.Context, in *UpdateDataSourceInput) error
	DeleteDataSource(ctx context.Context, indexID, id string) error
	ListDataSources(ctx context.Context, indexID string, page Page) (dataSources []DataSource, nextToken string, err error)

	TagResource(ctx context.Context, resourceARN string, tags []Tag) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) ([]Tag, error)
}
