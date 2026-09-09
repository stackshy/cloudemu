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

// UpdateRouteInput carries the mutable fields UpdateRoute accepts.
type UpdateRouteInput struct {
	RouteKey          *string
	Target            *string
	AuthorizationType *string
	APIKeyRequired    *bool
	AuthorizerID      *string
	OperationName     *string
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
}

// CreateStageInput carries the fields CreateStage accepts.
type CreateStageInput struct {
	StageName            string
	Description          string
	AutoDeploy           bool
	DeploymentID         string
	StageVariables       map[string]string
	DefaultRouteSettings *RouteSettings
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
// Route, Integration and Stage sub-collections.
type APIGatewayV2 interface {
	CreateAPI(ctx context.Context, in *CreateAPIInput) (*API, error)
	GetAPI(ctx context.Context, apiID string) (*API, error)
	GetAPIs(ctx context.Context) ([]API, error)
	UpdateAPI(ctx context.Context, apiID string, in *UpdateAPIInput) (*API, error)
	DeleteAPI(ctx context.Context, apiID string) error

	CreateRoute(ctx context.Context, apiID string, in *CreateRouteInput) (*Route, error)
	GetRoute(ctx context.Context, apiID, routeID string) (*Route, error)
	GetRoutes(ctx context.Context, apiID string) ([]Route, error)
	UpdateRoute(ctx context.Context, apiID, routeID string, in *UpdateRouteInput) (*Route, error)
	DeleteRoute(ctx context.Context, apiID, routeID string) error

	CreateIntegration(ctx context.Context, apiID string, in *CreateIntegrationInput) (*Integration, error)
	GetIntegration(ctx context.Context, apiID, integrationID string) (*Integration, error)
	GetIntegrations(ctx context.Context, apiID string) ([]Integration, error)
	UpdateIntegration(ctx context.Context, apiID, integrationID string, in *UpdateIntegrationInput) (*Integration, error)
	DeleteIntegration(ctx context.Context, apiID, integrationID string) error

	CreateStage(ctx context.Context, apiID string, in *CreateStageInput) (*Stage, error)
	GetStage(ctx context.Context, apiID, stageName string) (*Stage, error)
	GetStages(ctx context.Context, apiID string) ([]Stage, error)
	UpdateStage(ctx context.Context, apiID, stageName string, in *UpdateStageInput) (*Stage, error)
	DeleteStage(ctx context.Context, apiID, stageName string) error
}
