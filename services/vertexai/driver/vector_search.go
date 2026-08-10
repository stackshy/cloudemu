package driver

import "context"

// IndexConfig describes a Vector Search index.
type IndexConfig struct {
	Location    string
	DisplayName string
	Description string
	Dimensions  int
}

// Index is a Vector Search index.
type Index struct {
	Name           string // projects/{p}/locations/{l}/indexes/{id}
	DisplayName    string
	Description    string
	Dimensions     int
	DatapointCount int
	CreateTime     string
	UpdateTime     string
}

// Datapoint is a single vector datapoint.
type Datapoint struct {
	DatapointID   string
	FeatureVector []float64
}

// IndexEndpointConfig describes a Vector Search index endpoint.
type IndexEndpointConfig struct {
	Location    string
	DisplayName string
	Description string
}

// DeployedIndex is an index deployed to an index endpoint.
type DeployedIndex struct {
	ID    string
	Index string
}

// IndexEndpoint serves nearest-neighbor queries.
type IndexEndpoint struct {
	Name            string // projects/{p}/locations/{l}/indexEndpoints/{id}
	DisplayName     string
	Description     string
	DeployedIndexes []DeployedIndex
	CreateTime      string
}

// Neighbor is a nearest-neighbor result.
type Neighbor struct {
	DatapointID string
	Distance    float64
}

// vectorSearchAPI covers indexes and index endpoints + the findNeighbors and
// upsert/remove data-plane calls.
type vectorSearchAPI interface {
	CreateIndex(ctx context.Context, cfg IndexConfig) (*Operation, *Index, error)
	GetIndex(ctx context.Context, name string) (*Index, error)
	ListIndexes(ctx context.Context, location string) ([]Index, error)
	DeleteIndex(ctx context.Context, name string) (*Operation, error)
	UpsertDatapoints(ctx context.Context, index string, datapoints []Datapoint) error
	RemoveDatapoints(ctx context.Context, index string, datapointIDs []string) error

	CreateIndexEndpoint(ctx context.Context, cfg IndexEndpointConfig) (*Operation, *IndexEndpoint, error)
	GetIndexEndpoint(ctx context.Context, name string) (*IndexEndpoint, error)
	ListIndexEndpoints(ctx context.Context, location string) ([]IndexEndpoint, error)
	DeleteIndexEndpoint(ctx context.Context, name string) (*Operation, error)
	DeployIndex(ctx context.Context, indexEndpoint string, di DeployedIndex) (*Operation, *IndexEndpoint, error)
	UndeployIndex(ctx context.Context, indexEndpoint, deployedIndexID string) (*Operation, *IndexEndpoint, error)
	FindNeighbors(ctx context.Context, indexEndpoint, deployedIndexID string, query []float64, count int) ([]Neighbor, error)
}
