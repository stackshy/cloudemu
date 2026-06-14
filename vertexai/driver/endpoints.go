package driver

import "context"

// EndpointConfig describes an endpoint to create.
type EndpointConfig struct {
	Location    string
	DisplayName string
	Description string
	Labels      map[string]string
}

// DeployedModel is a model deployed to an endpoint.
type DeployedModel struct {
	ID              string
	Model           string // model resource name
	DisplayName     string
	MachineType     string
	MinReplicaCount int
	MaxReplicaCount int
}

// Endpoint serves online predictions for one or more deployed models.
type Endpoint struct {
	Name           string // projects/{p}/locations/{l}/endpoints/{id}
	DisplayName    string
	Description    string
	DeployedModels []DeployedModel
	TrafficSplit   map[string]int
	Labels         map[string]string
	CreateTime     string
	UpdateTime     string
}

// PredictRequest is an online prediction request.
type PredictRequest struct {
	Endpoint   string
	Instances  []any
	Parameters any
}

// PredictResponse carries model predictions.
type PredictResponse struct {
	Predictions      []any
	DeployedModelID  string
	Model            string
	ModelDisplayName string
}

// endpointsAPI covers endpoints, model deployment, and online prediction.
type endpointsAPI interface {
	CreateEndpoint(ctx context.Context, cfg EndpointConfig) (*Operation, *Endpoint, error)
	GetEndpoint(ctx context.Context, name string) (*Endpoint, error)
	ListEndpoints(ctx context.Context, location string) ([]Endpoint, error)
	DeleteEndpoint(ctx context.Context, name string) (*Operation, error)

	DeployModel(ctx context.Context, endpoint string, dm DeployedModel) (*Operation, *Endpoint, error)
	UndeployModel(ctx context.Context, endpoint, deployedModelID string) (*Operation, *Endpoint, error)

	Predict(ctx context.Context, req PredictRequest) (*PredictResponse, error)
	RawPredict(ctx context.Context, endpoint string, body []byte) ([]byte, error)
}
