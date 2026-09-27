package apigatewayv2

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/apigatewayv2/driver"
)

// corsWire is the CORS configuration wire object (shared by request and response).
type corsWire struct {
	AllowCredentials bool     `json:"allowCredentials,omitempty"`
	AllowHeaders     []string `json:"allowHeaders,omitempty"`
	AllowMethods     []string `json:"allowMethods,omitempty"`
	AllowOrigins     []string `json:"allowOrigins,omitempty"`
	ExposeHeaders    []string `json:"exposeHeaders,omitempty"`
	MaxAge           int      `json:"maxAge,omitempty"`
}

func corsToDriver(c *corsWire) *driver.Cors {
	if c == nil {
		return nil
	}

	return &driver.Cors{
		AllowCredentials: c.AllowCredentials, AllowHeaders: c.AllowHeaders,
		AllowMethods: c.AllowMethods, AllowOrigins: c.AllowOrigins,
		ExposeHeaders: c.ExposeHeaders, MaxAge: c.MaxAge,
	}
}

func corsFromDriver(c *driver.Cors) *corsWire {
	if c == nil {
		return nil
	}

	return &corsWire{
		AllowCredentials: c.AllowCredentials, AllowHeaders: c.AllowHeaders,
		AllowMethods: c.AllowMethods, AllowOrigins: c.AllowOrigins,
		ExposeHeaders: c.ExposeHeaders, MaxAge: c.MaxAge,
	}
}

// createAPIRequest is the CreateApi request body.
type createAPIRequest struct {
	Name                      string            `json:"name"`
	ProtocolType              string            `json:"protocolType"`
	Description               string            `json:"description"`
	Version                   string            `json:"version"`
	RouteSelectionExpression  string            `json:"routeSelectionExpression"`
	APIKeySelectionExpression string            `json:"apiKeySelectionExpression"`
	DisableExecuteAPIEndpoint bool              `json:"disableExecuteApiEndpoint"`
	CorsConfiguration         *corsWire         `json:"corsConfiguration"`
	Tags                      map[string]string `json:"tags"`
	Target                    string            `json:"target"`
	RouteKey                  string            `json:"routeKey"`
	CredentialsArn            string            `json:"credentialsArn"`
}

// updateAPIRequest is the UpdateApi (PATCH) request body. Pointer fields
// distinguish an omitted field from a zero value so a PATCH only touches what
// the client sent.
type updateAPIRequest struct {
	Name                      *string   `json:"name"`
	Description               *string   `json:"description"`
	Version                   *string   `json:"version"`
	RouteSelectionExpression  *string   `json:"routeSelectionExpression"`
	APIKeySelectionExpression *string   `json:"apiKeySelectionExpression"`
	DisableExecuteAPIEndpoint *bool     `json:"disableExecuteApiEndpoint"`
	CorsConfiguration         *corsWire `json:"corsConfiguration"`
	Target                    *string   `json:"target"`
	RouteKey                  *string   `json:"routeKey"`
	CredentialsArn            *string   `json:"credentialsArn"`
}

// apiResponse is the API wire object.
type apiResponse struct {
	APIID                     string            `json:"apiId"`
	Name                      string            `json:"name"`
	ProtocolType              string            `json:"protocolType"`
	Description               string            `json:"description,omitempty"`
	Version                   string            `json:"version,omitempty"`
	RouteSelectionExpression  string            `json:"routeSelectionExpression"`
	APIKeySelectionExpression string            `json:"apiKeySelectionExpression"`
	DisableExecuteAPIEndpoint bool              `json:"disableExecuteApiEndpoint"`
	APIEndpoint               string            `json:"apiEndpoint"`
	CreatedDate               string            `json:"createdDate"`
	CorsConfiguration         *corsWire         `json:"corsConfiguration,omitempty"`
	Tags                      map[string]string `json:"tags"`
}

func toAPIResponse(a *driver.API) apiResponse {
	return apiResponse{
		APIID: a.APIID, Name: a.Name, ProtocolType: a.ProtocolType,
		Description: a.Description, Version: a.Version,
		RouteSelectionExpression:  a.RouteSelectionExpression,
		APIKeySelectionExpression: a.APIKeySelectionExpression,
		DisableExecuteAPIEndpoint: a.DisableExecuteAPIEndpoint,
		APIEndpoint:               a.APIEndpoint,
		CreatedDate:               isoTime(a.CreatedDate),
		CorsConfiguration:         corsFromDriver(a.CorsConfiguration),
		Tags:                      tagsOrEmpty(a.Tags),
	}
}

// tagsOrEmpty renders a nil tag map as {} so every resource carries "tags".
func tagsOrEmpty(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}

	return tags
}

// routeRequest is the CreateRoute/UpdateRoute request body. On create the
// pointer/value split does not matter; UpdateRoute reads only the pointers.
type routeRequest struct {
	RouteKey            string   `json:"routeKey"`
	Target              string   `json:"target"`
	AuthorizationType   string   `json:"authorizationType"`
	APIKeyRequired      bool     `json:"apiKeyRequired"`
	AuthorizerID        string   `json:"authorizerId"`
	AuthorizationScopes []string `json:"authorizationScopes"`
	OperationName       string   `json:"operationName"`
}

// updateRouteRequest is the UpdateRoute (PATCH) request body.
type updateRouteRequest struct {
	RouteKey            *string  `json:"routeKey"`
	Target              *string  `json:"target"`
	AuthorizationType   *string  `json:"authorizationType"`
	APIKeyRequired      *bool    `json:"apiKeyRequired"`
	AuthorizerID        *string  `json:"authorizerId"`
	OperationName       *string  `json:"operationName"`
	AuthorizationScopes []string `json:"authorizationScopes"`
}

// routeResponse is the Route wire object.
type routeResponse struct {
	RouteID             string   `json:"routeId"`
	RouteKey            string   `json:"routeKey"`
	Target              string   `json:"target,omitempty"`
	AuthorizationType   string   `json:"authorizationType,omitempty"`
	APIKeyRequired      bool     `json:"apiKeyRequired"`
	AuthorizerID        string   `json:"authorizerId,omitempty"`
	AuthorizationScopes []string `json:"authorizationScopes,omitempty"`
	OperationName       string   `json:"operationName,omitempty"`
	APIGatewayManaged   bool     `json:"apiGatewayManaged,omitempty"`
}

func toRouteResponse(r *driver.Route) routeResponse {
	return routeResponse{
		RouteID: r.RouteID, RouteKey: r.RouteKey, Target: r.Target,
		AuthorizationType: r.AuthorizationType, APIKeyRequired: r.APIKeyRequired,
		AuthorizerID: r.AuthorizerID, AuthorizationScopes: r.AuthorizationScopes,
		OperationName: r.OperationName, APIGatewayManaged: r.APIGatewayManaged,
	}
}

// integrationRequest is the CreateIntegration request body.
type integrationRequest struct {
	IntegrationType      string            `json:"integrationType"`
	IntegrationURI       string            `json:"integrationUri"`
	IntegrationMethod    string            `json:"integrationMethod"`
	ConnectionType       string            `json:"connectionType"`
	PayloadFormatVersion string            `json:"payloadFormatVersion"`
	TimeoutInMillis      int               `json:"timeoutInMillis"`
	Description          string            `json:"description"`
	RequestParameters    map[string]string `json:"requestParameters"`
	CredentialsArn       string            `json:"credentialsArn"`

	RequestTemplates            map[string]string `json:"requestTemplates"`
	TemplateSelectionExpression string            `json:"templateSelectionExpression"`
	PassthroughBehavior         string            `json:"passthroughBehavior"`
}

// updateIntegrationRequest is the UpdateIntegration (PATCH) request body.
type updateIntegrationRequest struct {
	IntegrationType      *string           `json:"integrationType"`
	IntegrationURI       *string           `json:"integrationUri"`
	IntegrationMethod    *string           `json:"integrationMethod"`
	ConnectionType       *string           `json:"connectionType"`
	PayloadFormatVersion *string           `json:"payloadFormatVersion"`
	TimeoutInMillis      *int              `json:"timeoutInMillis"`
	Description          *string           `json:"description"`
	RequestParameters    map[string]string `json:"requestParameters"`
	CredentialsArn       *string           `json:"credentialsArn"`

	RequestTemplates            map[string]string `json:"requestTemplates"`
	TemplateSelectionExpression *string           `json:"templateSelectionExpression"`
	PassthroughBehavior         *string           `json:"passthroughBehavior"`
}

// integrationResponse is the Integration wire object.
type integrationResponse struct {
	IntegrationID        string            `json:"integrationId"`
	IntegrationType      string            `json:"integrationType"`
	IntegrationURI       string            `json:"integrationUri,omitempty"`
	IntegrationMethod    string            `json:"integrationMethod,omitempty"`
	ConnectionType       string            `json:"connectionType,omitempty"`
	PayloadFormatVersion string            `json:"payloadFormatVersion,omitempty"`
	TimeoutInMillis      int               `json:"timeoutInMillis,omitempty"`
	Description          string            `json:"description,omitempty"`
	RequestParameters    map[string]string `json:"requestParameters,omitempty"`
	CredentialsArn       string            `json:"credentialsArn,omitempty"`
	APIGatewayManaged    bool              `json:"apiGatewayManaged,omitempty"`

	RequestTemplates            map[string]string `json:"requestTemplates,omitempty"`
	TemplateSelectionExpression string            `json:"templateSelectionExpression,omitempty"`
	PassthroughBehavior         string            `json:"passthroughBehavior,omitempty"`
}

func toIntegrationResponse(i *driver.Integration) integrationResponse {
	return integrationResponse{
		IntegrationID: i.IntegrationID, IntegrationType: i.IntegrationType,
		IntegrationURI: i.IntegrationURI, IntegrationMethod: i.IntegrationMethod,
		ConnectionType: i.ConnectionType, PayloadFormatVersion: i.PayloadFormatVersion,
		TimeoutInMillis: i.TimeoutInMillis, Description: i.Description,
		RequestParameters: i.RequestParameters, CredentialsArn: i.CredentialsArn,
		APIGatewayManaged: i.APIGatewayManaged,

		RequestTemplates:            i.RequestTemplates,
		TemplateSelectionExpression: i.TemplateSelectionExpression,
		PassthroughBehavior:         i.PassthroughBehavior,
	}
}

// routeSettingsWire is the (default) route settings wire object.
type routeSettingsWire struct {
	DetailedMetricsEnabled bool    `json:"detailedMetricsEnabled,omitempty"`
	ThrottlingBurstLimit   int     `json:"throttlingBurstLimit,omitempty"`
	ThrottlingRateLimit    float64 `json:"throttlingRateLimit,omitempty"`
	LoggingLevel           string  `json:"loggingLevel,omitempty"`
	DataTraceEnabled       bool    `json:"dataTraceEnabled,omitempty"`
}

func routeSettingsToDriver(s *routeSettingsWire) *driver.RouteSettings {
	if s == nil {
		return nil
	}

	return &driver.RouteSettings{
		DetailedMetricsEnabled: s.DetailedMetricsEnabled, ThrottlingBurstLimit: s.ThrottlingBurstLimit,
		ThrottlingRateLimit: s.ThrottlingRateLimit, LoggingLevel: s.LoggingLevel,
		DataTraceEnabled: s.DataTraceEnabled,
	}
}

func routeSettingsFromDriver(s *driver.RouteSettings) *routeSettingsWire {
	if s == nil {
		return nil
	}

	return &routeSettingsWire{
		DetailedMetricsEnabled: s.DetailedMetricsEnabled, ThrottlingBurstLimit: s.ThrottlingBurstLimit,
		ThrottlingRateLimit: s.ThrottlingRateLimit, LoggingLevel: s.LoggingLevel,
		DataTraceEnabled: s.DataTraceEnabled,
	}
}

// stageRequest is the CreateStage request body.
type stageRequest struct {
	StageName            string             `json:"stageName"`
	Description          string             `json:"description"`
	AutoDeploy           bool               `json:"autoDeploy"`
	DeploymentID         string             `json:"deploymentId"`
	StageVariables       map[string]string  `json:"stageVariables"`
	DefaultRouteSettings *routeSettingsWire `json:"defaultRouteSettings"`
	Tags                 map[string]string  `json:"tags"`
}

// updateStageRequest is the UpdateStage (PATCH) request body.
type updateStageRequest struct {
	Description          *string            `json:"description"`
	AutoDeploy           *bool              `json:"autoDeploy"`
	DeploymentID         *string            `json:"deploymentId"`
	StageVariables       map[string]string  `json:"stageVariables"`
	DefaultRouteSettings *routeSettingsWire `json:"defaultRouteSettings"`
}

// stageResponse is the Stage wire object.
type stageResponse struct {
	StageName            string             `json:"stageName"`
	Description          string             `json:"description,omitempty"`
	AutoDeploy           bool               `json:"autoDeploy"`
	DeploymentID         string             `json:"deploymentId,omitempty"`
	StageVariables       map[string]string  `json:"stageVariables,omitempty"`
	DefaultRouteSettings *routeSettingsWire `json:"defaultRouteSettings,omitempty"`
	CreatedDate          string             `json:"createdDate"`
	LastUpdatedDate      string             `json:"lastUpdatedDate"`
	Tags                 map[string]string  `json:"tags"`
	APIGatewayManaged    bool               `json:"apiGatewayManaged,omitempty"`

	LastDeploymentStatusMessage string `json:"lastDeploymentStatusMessage,omitempty"`
}

func toStageResponse(s *driver.Stage) stageResponse {
	return stageResponse{
		StageName: s.StageName, Description: s.Description, AutoDeploy: s.AutoDeploy,
		DeploymentID: s.DeploymentID, StageVariables: s.StageVariables,
		DefaultRouteSettings: routeSettingsFromDriver(s.DefaultRouteSettings),
		CreatedDate:          isoTime(s.CreatedDate), LastUpdatedDate: isoTime(s.LastUpdatedDate),
		Tags: tagsOrEmpty(s.Tags), APIGatewayManaged: s.APIGatewayManaged,

		LastDeploymentStatusMessage: s.LastDeploymentStatusMessage,
	}
}

// deploymentRequest is the CreateDeployment request body.
type deploymentRequest struct {
	Description string `json:"description"`
	StageName   string `json:"stageName"`
}

// updateDeploymentRequest is the UpdateDeployment (PATCH) request body.
type updateDeploymentRequest struct {
	Description *string `json:"description"`
}

// deploymentResponse is the Deployment wire object.
type deploymentResponse struct {
	DeploymentID            string `json:"deploymentId"`
	Description             string `json:"description,omitempty"`
	CreatedDate             string `json:"createdDate"`
	DeploymentStatus        string `json:"deploymentStatus"`
	DeploymentStatusMessage string `json:"deploymentStatusMessage,omitempty"`
	AutoDeployed            bool   `json:"autoDeployed"`
}

func toDeploymentResponse(d *driver.Deployment) deploymentResponse {
	return deploymentResponse{
		DeploymentID: d.DeploymentID, Description: d.Description, CreatedDate: isoTime(d.CreatedDate),
		DeploymentStatus: d.DeploymentStatus, DeploymentStatusMessage: d.DeploymentStatusMessage,
		AutoDeployed: d.AutoDeployed,
	}
}

// tagsBody is the TagResource request body and the GetTags response body.
type tagsBody struct {
	Tags map[string]string `json:"tags"`
}

// isoTime renders a unix-seconds timestamp as the ISO8601 string the
// apigatewayv2 restJson1 protocol uses for timestamp fields (createdDate,
// lastUpdatedDate).
func isoTime(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}
