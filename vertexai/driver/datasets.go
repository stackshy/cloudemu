package driver

import "context"

// DatasetConfig describes a dataset to create.
type DatasetConfig struct {
	Location          string
	DisplayName       string
	MetadataSchemaURI string
	Labels            map[string]string
}

// Dataset is a Vertex AI dataset.
type Dataset struct {
	Name              string // projects/{p}/locations/{l}/datasets/{id}
	DisplayName       string
	MetadataSchemaURI string
	Labels            map[string]string
	CreateTime        string
	UpdateTime        string
}

// datasetsAPI covers dataset lifecycle plus import/export (modeled as
// already-done operations).
type datasetsAPI interface {
	CreateDataset(ctx context.Context, cfg DatasetConfig) (*Operation, *Dataset, error)
	GetDataset(ctx context.Context, name string) (*Dataset, error)
	ListDatasets(ctx context.Context, location string) ([]Dataset, error)
	PatchDataset(ctx context.Context, name, displayName string) (*Dataset, error)
	DeleteDataset(ctx context.Context, name string) (*Operation, error)
	ImportData(ctx context.Context, name, gcsURI string) (*Operation, error)
	ExportData(ctx context.Context, name, gcsOutputURI string) (*Operation, error)
}
