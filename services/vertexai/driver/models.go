package driver

import "context"

// ModelConfig describes a model to upload to the registry.
type ModelConfig struct {
	Location       string
	DisplayName    string
	Description    string
	ContainerImage string
	ArtifactURI    string
	Labels         map[string]string
}

// Model is a registered model. Versions are addressed via the @version suffix
// on the resource name rather than a sub-collection.
type Model struct {
	Name           string // projects/{p}/locations/{l}/models/{id}
	DisplayName    string
	Description    string
	ContainerImage string
	ArtifactURI    string
	VersionID      string
	VersionAliases []string
	Labels         map[string]string
	CreateTime     string
	UpdateTime     string
}

// ModelEvaluation is an evaluation attached to a model version.
type ModelEvaluation struct {
	Name        string // .../models/{id}/evaluations/{eid}
	DisplayName string
	MetricsType string
	CreateTime  string
}

// modelsAPI covers the model registry (upload/get/list/patch/delete + versions
// + evaluations).
type modelsAPI interface {
	UploadModel(ctx context.Context, cfg ModelConfig) (*Operation, *Model, error)
	GetModel(ctx context.Context, name string) (*Model, error)
	ListModels(ctx context.Context, location string) ([]Model, error)
	PatchModel(ctx context.Context, name, displayName, description string) (*Model, error)
	DeleteModel(ctx context.Context, name string) (*Operation, error)

	ListModelVersions(ctx context.Context, name string) ([]Model, error)
	DeleteModelVersion(ctx context.Context, name string) (*Operation, error)

	ImportModelEvaluation(ctx context.Context, modelName string, eval ModelEvaluation) (*ModelEvaluation, error)
	GetModelEvaluation(ctx context.Context, name string) (*ModelEvaluation, error)
	ListModelEvaluations(ctx context.Context, modelName string) ([]ModelEvaluation, error)
}
