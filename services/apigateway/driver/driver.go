// Package driver defines the API Gateway service contract: the REST API (v1)
// control-plane model (RestApi -> Resource tree -> Method -> Integration ->
// Deployment -> Stage) plus the data-plane routing entry point InvokeRoute,
// which resolves a request's method+path against a deployed stage and, for a
// Lambda proxy integration, invokes the target function and returns its mapped
// HTTP response.
//
// Only AWS implements this driver today (Amazon API Gateway); the interface is
// shaped to the AWS REST API v1 protocol.
package driver

import "context"

// Integration types. AWS_PROXY (Lambda proxy) is the fully-wired path: the
// whole request is passed to the function as a proxy event and the function's
// {statusCode,headers,body} response is returned verbatim.
const (
	IntegrationAWSProxy  = "AWS_PROXY"
	IntegrationAWS       = "AWS"
	IntegrationHTTP      = "HTTP"
	IntegrationHTTPProxy = "HTTP_PROXY"
	IntegrationMock      = "MOCK"
)

// MethodANY is the catch-all HTTP method a Method/Integration can be defined
// under; it matches any request method when no method-specific entry exists.
const MethodANY = "ANY"

// RestAPI is an API Gateway REST API (v1) control-plane object.
type RestAPI struct {
	ID                         string
	Name                       string
	Description                string
	Version                    string
	CreatedDate                int64 // unix seconds
	RootResourceID             string
	APIKeySource               string
	Tags                       map[string]string
	BinaryMediaTypes           []string
	EndpointConfigurationTypes []string
}

// Resource is a node in a REST API's path tree. Path is the fully-resolved
// path from the root (e.g. "/pets/{petId}"); PathPart is this node's own
// segment (e.g. "{petId}"). Methods maps an HTTP method (or "ANY") to its
// Method definition.
type Resource struct {
	ID        string
	RestAPIID string
	ParentID  string
	PathPart  string
	Path      string
	Methods   map[string]*Method
}

// Method is an HTTP method configured on a Resource, optionally wired to an
// Integration.
type Method struct {
	HTTPMethod        string
	AuthorizationType string
	APIKeyRequired    bool
	Integration       *Integration
}

// Integration is the backend a Method forwards to. For AWS_PROXY/AWS the URI is
// the Lambda invocation ARN
// (arn:aws:apigateway:<region>:lambda:path/2015-03-31/functions/<function-arn>/invocations).
type Integration struct {
	Type                  string
	IntegrationHTTPMethod string
	URI                   string
	PassthroughBehavior   string
}

// Deployment is a point-in-time snapshot of a REST API published to a stage.
type Deployment struct {
	ID          string
	RestAPIID   string
	Description string
	CreatedDate int64
}

// Stage is a named, addressable deployment of a REST API (e.g. "prod").
type Stage struct {
	StageName    string
	RestAPIID    string
	DeploymentID string
	Description  string
	CreatedDate  int64
	Variables    map[string]string
}

// CreateRestAPIInput carries the fields CreateRestApi accepts.
type CreateRestAPIInput struct {
	Name                       string
	Description                string
	Version                    string
	APIKeySource               string
	Tags                       map[string]string
	BinaryMediaTypes           []string
	EndpointConfigurationTypes []string
}

// PutMethodInput carries the fields PutMethod accepts.
type PutMethodInput struct {
	AuthorizationType string
	APIKeyRequired    bool
}

// PutIntegrationInput carries the fields PutIntegration accepts.
type PutIntegrationInput struct {
	Type                  string
	IntegrationHTTPMethod string
	URI                   string
	PassthroughBehavior   string
}

// CreateDeploymentInput carries the fields CreateDeployment accepts. A non-empty
// StageName auto-creates (or re-points) that stage to the new deployment, exactly
// as the real CreateDeployment does.
type CreateDeploymentInput struct {
	StageName   string
	Description string
}

// CreateStageInput carries the fields CreateStage accepts.
type CreateStageInput struct {
	StageName    string
	DeploymentID string
	Description  string
	Variables    map[string]string
}

// ProxyRequest is a data-plane request to route through a deployed stage.
type ProxyRequest struct {
	RestAPIID         string
	StageName         string
	HTTPMethod        string
	Path              string // resource path relative to the stage, leading "/"
	Headers           map[string]string
	MultiValueHeaders map[string][]string
	Query             map[string]string
	MultiValueQuery   map[string][]string
	Body              string
	IsBase64Encoded   bool
	SourceIP          string
	Host              string
	Protocol          string
}

// ProxyResponse is the HTTP response produced by routing a ProxyRequest,
// mapped from the Lambda proxy integration's {statusCode,headers,body} output.
type ProxyResponse struct {
	StatusCode        int
	Headers           map[string]string
	MultiValueHeaders map[string][]string
	Body              string
	IsBase64Encoded   bool
}

// APIGateway is the interface an API Gateway provider implements: the REST API
// v1 control plane plus the InvokeRoute data-plane entry point.
type APIGateway interface {
	CreateRestAPI(ctx context.Context, in *CreateRestAPIInput) (*RestAPI, error)
	GetRestAPIs(ctx context.Context) ([]RestAPI, error)
	GetRestAPI(ctx context.Context, id string) (*RestAPI, error)
	DeleteRestAPI(ctx context.Context, id string) error

	CreateResource(ctx context.Context, restAPIID, parentID, pathPart string) (*Resource, error)
	GetResources(ctx context.Context, restAPIID string) ([]Resource, error)
	GetResource(ctx context.Context, restAPIID, resourceID string) (*Resource, error)

	PutMethod(ctx context.Context, restAPIID, resourceID, httpMethod string, in PutMethodInput) (*Method, error)
	GetMethod(ctx context.Context, restAPIID, resourceID, httpMethod string) (*Method, error)

	PutIntegration(ctx context.Context, restAPIID, resourceID, httpMethod string, in PutIntegrationInput) (*Integration, error)
	GetIntegration(ctx context.Context, restAPIID, resourceID, httpMethod string) (*Integration, error)

	CreateDeployment(ctx context.Context, restAPIID string, in CreateDeploymentInput) (*Deployment, error)
	CreateStage(ctx context.Context, restAPIID string, in CreateStageInput) (*Stage, error)
	GetStage(ctx context.Context, restAPIID, stageName string) (*Stage, error)

	// InvokeRoute resolves req.HTTPMethod+req.Path against the deployed stage's
	// resource tree ({proxy+} greedy paths and {param} placeholders supported)
	// and, for an AWS_PROXY/AWS Lambda integration, invokes the target function
	// and returns its mapped HTTP response.
	InvokeRoute(ctx context.Context, req *ProxyRequest) (*ProxyResponse, error)
}
