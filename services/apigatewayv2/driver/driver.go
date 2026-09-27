// Package driver defines the Amazon API Gateway v2 (HTTP/WebSocket APIs)
// control-plane contract: the API -> Route/Integration/Stage model reachable
// over the apigatewayv2 REST/JSON protocol rooted at /v2/apis. This is a
// distinct service from API Gateway REST v1 (services/apigateway, /restapis);
// the two share no types or endpoints.
//
// Only AWS implements this driver today; the interface is shaped to the AWS
// apigatewayv2 REST API protocol.
package driver

import "context"

// Protocol types an API can declare.
const (
	ProtocolHTTP      = "HTTP"
	ProtocolWebSocket = "WEBSOCKET"
)

// Integration types a Route's backend Integration can be.
const (
	IntegrationAWSProxy  = "AWS_PROXY"
	IntegrationHTTPProxy = "HTTP_PROXY"
	IntegrationAWS       = "AWS"
	IntegrationHTTP      = "HTTP"
	IntegrationMock      = "MOCK"
)

// Deployment statuses.
const (
	DeploymentStatusPending  = "PENDING"
	DeploymentStatusFailed   = "FAILED"
	DeploymentStatusDeployed = "DEPLOYED"
)

// PageInput carries the MaxResults and NextToken query parameters every
// apigatewayv2 list operation accepts. MaxResults is a string on the wire, so
// it stays one here and the provider validates it.
type PageInput struct {
	MaxResults string
	NextToken  string
}

// API is an apigatewayv2 API (HTTP or WebSocket) control-plane object.
type API struct {
	APIID                     string
	Name                      string
	ProtocolType              string
	Description               string
	Version                   string
	RouteSelectionExpression  string
	APIKeySelectionExpression string
	DisableExecuteAPIEndpoint bool
	APIEndpoint               string
	CreatedDate               int64 // unix seconds
	Tags                      map[string]string
	CorsConfiguration         *Cors
}

// Cors is an HTTP API's CORS configuration.
type Cors struct {
	AllowCredentials bool
	AllowHeaders     []string
	AllowMethods     []string
	AllowOrigins     []string
	ExposeHeaders    []string
	MaxAge           int
}

// Route is a route on an API. RouteKey is e.g. "GET /items" or "$default";
// Target is e.g. "integrations/{integrationId}".
type Route struct {
	RouteID             string
	RouteKey            string
	Target              string
	AuthorizationType   string
	APIKeyRequired      bool
	AuthorizerID        string
	AuthorizationScopes []string
	OperationName       string
	APIGatewayManaged   bool
}

// Integration is a backend an API's routes forward to.
type Integration struct {
	IntegrationID        string
	IntegrationType      string
	IntegrationURI       string
	IntegrationMethod    string
	ConnectionType       string
	PayloadFormatVersion string
	TimeoutInMillis      int
	Description          string
	RequestParameters    map[string]string
	CredentialsArn       string
	APIGatewayManaged    bool

	RequestTemplates            map[string]string
	TemplateSelectionExpression string
	PassthroughBehavior         string
}

// Stage is a named deployment stage of an API (e.g. "$default", "prod").
type Stage struct {
	StageName            string
	Description          string
	AutoDeploy           bool
	DeploymentID         string
	StageVariables       map[string]string
	DefaultRouteSettings *RouteSettings
	CreatedDate          int64 // unix seconds
	LastUpdatedDate      int64 // unix seconds
	Tags                 map[string]string
	APIGatewayManaged    bool

	LastDeploymentStatusMessage string
}

// Deployment is an immutable snapshot of an API's routes and integrations
// that a stage serves.
type Deployment struct {
	DeploymentID            string
	Description             string
	CreatedDate             int64 // unix seconds
	DeploymentStatus        string
	DeploymentStatusMessage string
	AutoDeployed            bool
}

// CreateDeploymentInput carries the fields CreateDeployment accepts. A
// non-empty StageName also points that stage at the new deployment.
type CreateDeploymentInput struct {
	Description string
	StageName   string
}

// UpdateDeploymentInput carries the mutable fields UpdateDeployment accepts.
type UpdateDeploymentInput struct {
	Description *string
}

// RouteSettings are the per-route (or default) execution settings on a Stage.
type RouteSettings struct {
	DetailedMetricsEnabled bool
	ThrottlingBurstLimit   int
	ThrottlingRateLimit    float64
	LoggingLevel           string
	DataTraceEnabled       bool
}

// CreateAPIInput carries the fields CreateApi accepts.
type CreateAPIInput struct {
	Name                      string
	ProtocolType              string
	Description               string
	Version                   string
	RouteSelectionExpression  string
	APIKeySelectionExpression string
	DisableExecuteAPIEndpoint bool
	Tags                      map[string]string
	CorsConfiguration         *Cors

	// Quick create: Target creates a managed integration, a route for
	// RouteKey (default "$default") and an auto-deployed $default stage.
	Target         string
	RouteKey       string
	CredentialsArn string
}

// UpdateAPIInput carries the mutable fields UpdateApi accepts. A nil pointer
// leaves the stored value unchanged (PATCH semantics).
type UpdateAPIInput struct {
	Name                      *string
	Description               *string
	Version                   *string
	RouteSelectionExpression  *string
	APIKeySelectionExpression *string
	DisableExecuteAPIEndpoint *bool
	CorsConfiguration         *Cors

	// Quick create fields; they update the managed integration and route.
	Target         *string
	RouteKey       *string
	CredentialsArn *string
}

// CreateRouteInput carries the fields CreateRoute accepts.
type CreateRouteInput struct {
	RouteKey            string
	Target              string
	AuthorizationType   string
	APIKeyRequired      bool
	AuthorizerID        string
	AuthorizationScopes []string
	OperationName       string
}

// UpdateRouteInput carries the mutable fields UpdateRoute accepts. A nil
// AuthorizationScopes leaves the stored scopes unchanged.
type UpdateRouteInput struct {
	RouteKey            *string
	Target              *string
	AuthorizationType   *string
	APIKeyRequired      *bool
	AuthorizerID        *string
	OperationName       *string
	AuthorizationScopes []string
}

// CreateIntegrationInput carries the fields CreateIntegration accepts.
type CreateIntegrationInput struct {
	IntegrationType      string
	IntegrationURI       string
	IntegrationMethod    string
	ConnectionType       string
	PayloadFormatVersion string
	TimeoutInMillis      int
	Description          string
	RequestParameters    map[string]string
	CredentialsArn       string

	RequestTemplates            map[string]string
	TemplateSelectionExpression string
	PassthroughBehavior         string
}

// UpdateIntegrationInput carries the mutable fields UpdateIntegration accepts.
type UpdateIntegrationInput struct {
	IntegrationType      *string
	IntegrationURI       *string
	IntegrationMethod    *string
	ConnectionType       *string
	PayloadFormatVersion *string
	TimeoutInMillis      *int
	Description          *string
	RequestParameters    map[string]string
	CredentialsArn       *string

	RequestTemplates            map[string]string
	TemplateSelectionExpression *string
	PassthroughBehavior         *string
}

// CreateStageInput carries the fields CreateStage accepts.
type CreateStageInput struct {
	StageName            string
	Description          string
	AutoDeploy           bool
	DeploymentID         string
	StageVariables       map[string]string
	DefaultRouteSettings *RouteSettings
	Tags                 map[string]string
}

// UpdateStageInput carries the mutable fields UpdateStage accepts.
type UpdateStageInput struct {
	Description          *string
	AutoDeploy           *bool
	DeploymentID         *string
	StageVariables       map[string]string
	DefaultRouteSettings *RouteSettings
}

// APIGatewayV2 is the apigatewayv2 control-plane contract: API CRUD plus its
// Route, Integration, Stage and Deployment sub-collections, and resource
// tagging. Every list returns one page plus the next token ("" on the last).
type APIGatewayV2 interface {
	CreateAPI(ctx context.Context, in *CreateAPIInput) (*API, error)
	GetAPI(ctx context.Context, apiID string) (*API, error)
	GetAPIs(ctx context.Context, page *PageInput) ([]API, string, error)
	UpdateAPI(ctx context.Context, apiID string, in *UpdateAPIInput) (*API, error)
	DeleteAPI(ctx context.Context, apiID string) error

	CreateRoute(ctx context.Context, apiID string, in *CreateRouteInput) (*Route, error)
	GetRoute(ctx context.Context, apiID, routeID string) (*Route, error)
	GetRoutes(ctx context.Context, apiID string, page *PageInput) ([]Route, string, error)
	UpdateRoute(ctx context.Context, apiID, routeID string, in *UpdateRouteInput) (*Route, error)
	DeleteRoute(ctx context.Context, apiID, routeID string) error

	CreateIntegration(ctx context.Context, apiID string, in *CreateIntegrationInput) (*Integration, error)
	GetIntegration(ctx context.Context, apiID, integrationID string) (*Integration, error)
	GetIntegrations(ctx context.Context, apiID string, page *PageInput) ([]Integration, string, error)
	UpdateIntegration(ctx context.Context, apiID, integrationID string, in *UpdateIntegrationInput) (*Integration, error)
	DeleteIntegration(ctx context.Context, apiID, integrationID string) error

	CreateStage(ctx context.Context, apiID string, in *CreateStageInput) (*Stage, error)
	GetStage(ctx context.Context, apiID, stageName string) (*Stage, error)
	GetStages(ctx context.Context, apiID string, page *PageInput) ([]Stage, string, error)
	UpdateStage(ctx context.Context, apiID, stageName string, in *UpdateStageInput) (*Stage, error)
	DeleteStage(ctx context.Context, apiID, stageName string) error

	CreateDeployment(ctx context.Context, apiID string, in *CreateDeploymentInput) (*Deployment, error)
	GetDeployment(ctx context.Context, apiID, deploymentID string) (*Deployment, error)
	GetDeployments(ctx context.Context, apiID string, page *PageInput) ([]Deployment, string, error)
	UpdateDeployment(ctx context.Context, apiID, deploymentID string, in *UpdateDeploymentInput) (*Deployment, error)
	DeleteDeployment(ctx context.Context, apiID, deploymentID string) error

	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	GetTags(ctx context.Context, resourceARN string) (map[string]string, error)
}
