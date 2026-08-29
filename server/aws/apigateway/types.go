package apigateway

import "github.com/stackshy/cloudemu/v2/services/apigateway/driver"

// createRestAPIRequest is the CreateRestApi request body (restJson1).
type createRestAPIRequest struct {
	Name                  string                 `json:"name"`
	Description           string                 `json:"description"`
	Version               string                 `json:"version"`
	APIKeySource          string                 `json:"apiKeySource"`
	BinaryMediaTypes      []string               `json:"binaryMediaTypes"`
	Tags                  map[string]string      `json:"tags"`
	EndpointConfiguration *endpointConfiguration `json:"endpointConfiguration"`
}

type endpointConfiguration struct {
	Types []string `json:"types"`
}

// createResourceRequest is the CreateResource request body.
type createResourceRequest struct {
	PathPart string `json:"pathPart"`
}

// putMethodRequest is the PutMethod request body.
type putMethodRequest struct {
	AuthorizationType string `json:"authorizationType"`
	APIKeyRequired    bool   `json:"apiKeyRequired"`
}

// putIntegrationRequest is the PutIntegration request body.
type putIntegrationRequest struct {
	Type                  string `json:"type"`
	IntegrationHTTPMethod string `json:"integrationHttpMethod"`
	URI                   string `json:"uri"`
	PassthroughBehavior   string `json:"passthroughBehavior"`
}

// createDeploymentRequest is the CreateDeployment request body.
type createDeploymentRequest struct {
	StageName   string `json:"stageName"`
	Description string `json:"description"`
}

// createStageRequest is the CreateStage request body.
type createStageRequest struct {
	StageName    string            `json:"stageName"`
	DeploymentID string            `json:"deploymentId"`
	Description  string            `json:"description"`
	Variables    map[string]string `json:"variables"`
}

// restAPIResponse is the RestApi wire object.
type restAPIResponse struct {
	ID                    string                 `json:"id"`
	Name                  string                 `json:"name"`
	Description           string                 `json:"description,omitempty"`
	Version               string                 `json:"version,omitempty"`
	CreatedDate           int64                  `json:"createdDate"`
	RootResourceID        string                 `json:"rootResourceId"`
	APIKeySource          string                 `json:"apiKeySource,omitempty"`
	Tags                  map[string]string      `json:"tags,omitempty"`
	BinaryMediaTypes      []string               `json:"binaryMediaTypes,omitempty"`
	EndpointConfiguration *endpointConfiguration `json:"endpointConfiguration,omitempty"`
}

// listRestAPIsResponse is the GetRestApis wire object.
type listRestAPIsResponse struct {
	Item []restAPIResponse `json:"item"`
}

// resourceResponse is the Resource wire object.
type resourceResponse struct {
	ID              string                    `json:"id"`
	ParentID        string                    `json:"parentId,omitempty"`
	PathPart        string                    `json:"pathPart,omitempty"`
	Path            string                    `json:"path"`
	ResourceMethods map[string]methodResponse `json:"resourceMethods,omitempty"`
}

// listResourcesResponse is the GetResources wire object.
type listResourcesResponse struct {
	Item []resourceResponse `json:"item"`
}

// methodResponse is the Method wire object.
type methodResponse struct {
	HTTPMethod        string               `json:"httpMethod,omitempty"`
	AuthorizationType string               `json:"authorizationType,omitempty"`
	APIKeyRequired    bool                 `json:"apiKeyRequired"`
	MethodIntegration *integrationResponse `json:"methodIntegration,omitempty"`
}

// integrationResponse is the Integration wire object.
type integrationResponse struct {
	Type                string `json:"type"`
	HTTPMethod          string `json:"httpMethod,omitempty"`
	URI                 string `json:"uri,omitempty"`
	PassthroughBehavior string `json:"passthroughBehavior,omitempty"`
}

// deploymentResponse is the Deployment wire object.
type deploymentResponse struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	CreatedDate int64  `json:"createdDate"`
}

// stageResponse is the Stage wire object.
type stageResponse struct {
	StageName    string            `json:"stageName"`
	DeploymentID string            `json:"deploymentId,omitempty"`
	Description  string            `json:"description,omitempty"`
	CreatedDate  int64             `json:"createdDate"`
	Variables    map[string]string `json:"variables,omitempty"`
}

func toRestAPIResponse(a *driver.RestAPI) restAPIResponse {
	resp := restAPIResponse{
		ID: a.ID, Name: a.Name, Description: a.Description, Version: a.Version,
		CreatedDate: a.CreatedDate, RootResourceID: a.RootResourceID,
		APIKeySource: a.APIKeySource, Tags: a.Tags, BinaryMediaTypes: a.BinaryMediaTypes,
	}
	if len(a.EndpointConfigurationTypes) > 0 {
		resp.EndpointConfiguration = &endpointConfiguration{Types: a.EndpointConfigurationTypes}
	}

	return resp
}

func toResourceResponse(r *driver.Resource) resourceResponse {
	resp := resourceResponse{ID: r.ID, ParentID: r.ParentID, PathPart: r.PathPart, Path: r.Path}

	if len(r.Methods) > 0 {
		resp.ResourceMethods = make(map[string]methodResponse, len(r.Methods))
		for name, mth := range r.Methods {
			resp.ResourceMethods[name] = toMethodResponse(mth)
		}
	}

	return resp
}

func toMethodResponse(mth *driver.Method) methodResponse {
	resp := methodResponse{
		HTTPMethod: mth.HTTPMethod, AuthorizationType: mth.AuthorizationType,
		APIKeyRequired: mth.APIKeyRequired,
	}

	if mth.Integration != nil {
		ig := toIntegrationResponse(mth.Integration)
		resp.MethodIntegration = &ig
	}

	return resp
}

func toIntegrationResponse(ig *driver.Integration) integrationResponse {
	return integrationResponse{
		Type: ig.Type, HTTPMethod: ig.IntegrationHTTPMethod,
		URI: ig.URI, PassthroughBehavior: ig.PassthroughBehavior,
	}
}

func toStageResponse(s *driver.Stage) stageResponse {
	return stageResponse{
		StageName: s.StageName, DeploymentID: s.DeploymentID, Description: s.Description,
		CreatedDate: s.CreatedDate, Variables: s.Variables,
	}
}
