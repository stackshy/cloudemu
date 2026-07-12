package driver

import "context"

// Index is a search index definition (data plane).
type Index struct {
	Name           string
	Fields         []Field
	DefaultScoring string
	ETag           string
}

// Field is one index field.
type Field struct {
	Name        string
	Type        string // Edm.String | Edm.Int32 | Collection(Edm.Single) | ...
	Key         bool
	Searchable  bool
	Filterable  bool
	Sortable    bool
	Facetable   bool
	Retrievable bool
	Dimensions  int // vector field width (0 = non-vector)
}

// IndexerConfig describes an indexer to create or update.
type IndexerConfig struct {
	Name           string
	DataSourceName string
	TargetIndex    string
	SkillsetName   string
	Schedule       string
}

// Indexer is a search indexer.
type Indexer struct {
	Name           string
	DataSourceName string
	TargetIndex    string
	SkillsetName   string
	Schedule       string
	ETag           string
}

// IndexerStatus is the execution status of an indexer.
type IndexerStatus struct {
	Name           string
	Status         string // running | success | error | reset
	LastResult     string
	ItemsProcessed int
	ItemsFailed    int
}

// DataSource is an indexer data source.
type DataSource struct {
	Name       string
	Type       string // azureblob | azuresql | cosmosdb | ...
	Container  string
	ConnString string
	ETag       string
}

// Skillset is an AI enrichment skillset.
type Skillset struct {
	Name        string
	Description string
	SkillCount  int
	ETag        string
}

// SynonymMap is a synonym map.
type SynonymMap struct {
	Name     string
	Format   string // solr
	Synonyms string
	ETag     string
}

// Alias maps an alias name to an index.
type Alias struct {
	Name    string
	Indexes []string
	ETag    string
}

// IndexAction is one document mutation in a batch.
type IndexAction struct {
	Action   string // upload | merge | mergeOrUpload | delete
	Document map[string]any
}

// IndexResult is the per-document outcome of a batch index call.
type IndexResult struct {
	Key        string
	Status     bool
	StatusCode int
	ErrorMsg   string
}

// SearchRequest is a document search query.
type SearchRequest struct {
	Search  string
	Filter  string
	Top     int
	Skip    int
	OrderBy string
	Select  []string
	Count   bool
}

// SearchResult is a single matched document with its score.
type SearchResult struct {
	Score    float64
	Document map[string]any
}

// SearchResponse is the result of a document search.
type SearchResponse struct {
	Count   int64 // -1 when not requested
	Results []SearchResult
}

// SuggestResult is one suggestion / autocomplete hit.
type SuggestResult struct {
	Text     string
	Document map[string]any
}

// ServiceStatistics summarizes counts and usage for a service's data plane.
type ServiceStatistics struct {
	DocumentCount   int64
	IndexCount      int
	IndexerCount    int
	DataSourceCount int
	StorageBytes    int64
}

// SearchDataPlane is the {service}.search.windows.net data-plane surface.
type SearchDataPlane interface {
	// Indexes.
	CreateOrUpdateIndex(ctx context.Context, service string, idx Index) (*Index, error)
	GetIndex(ctx context.Context, service, name string) (*Index, error)
	ListIndexes(ctx context.Context, service string) ([]Index, error)
	DeleteIndex(ctx context.Context, service, name string) error

	// Documents.
	IndexDocuments(ctx context.Context, service, index string, actions []IndexAction) ([]IndexResult, error)
	SearchDocuments(ctx context.Context, service, index string, req SearchRequest) (*SearchResponse, error)
	SuggestDocuments(ctx context.Context, service, index, searchText, suggester string, top int) ([]SuggestResult, error)
	AutocompleteDocuments(ctx context.Context, service, index, searchText, suggester string, top int) ([]string, error)
	CountDocuments(ctx context.Context, service, index string) (int64, error)
	GetDocument(ctx context.Context, service, index, key string) (map[string]any, error)

	// Indexers.
	CreateOrUpdateIndexer(ctx context.Context, service string, cfg IndexerConfig) (*Indexer, error)
	GetIndexer(ctx context.Context, service, name string) (*Indexer, error)
	ListIndexers(ctx context.Context, service string) ([]Indexer, error)
	DeleteIndexer(ctx context.Context, service, name string) error
	RunIndexer(ctx context.Context, service, name string) error
	ResetIndexer(ctx context.Context, service, name string) error
	GetIndexerStatus(ctx context.Context, service, name string) (*IndexerStatus, error)

	// Data sources.
	CreateOrUpdateDataSource(ctx context.Context, service string, ds DataSource) (*DataSource, error)
	GetDataSource(ctx context.Context, service, name string) (*DataSource, error)
	ListDataSources(ctx context.Context, service string) ([]DataSource, error)
	DeleteDataSource(ctx context.Context, service, name string) error

	// Skillsets.
	CreateOrUpdateSkillset(ctx context.Context, service string, sk Skillset) (*Skillset, error)
	GetSkillset(ctx context.Context, service, name string) (*Skillset, error)
	ListSkillsets(ctx context.Context, service string) ([]Skillset, error)
	DeleteSkillset(ctx context.Context, service, name string) error

	// Synonym maps.
	CreateOrUpdateSynonymMap(ctx context.Context, service string, sm SynonymMap) (*SynonymMap, error)
	GetSynonymMap(ctx context.Context, service, name string) (*SynonymMap, error)
	ListSynonymMaps(ctx context.Context, service string) ([]SynonymMap, error)
	DeleteSynonymMap(ctx context.Context, service, name string) error

	// Aliases.
	CreateOrUpdateAlias(ctx context.Context, service string, alias Alias) (*Alias, error)
	GetAlias(ctx context.Context, service, name string) (*Alias, error)
	ListAliases(ctx context.Context, service string) ([]Alias, error)
	DeleteAlias(ctx context.Context, service, name string) error

	// Service statistics.
	GetServiceStatistics(ctx context.Context, service string) (*ServiceStatistics, error)
}
