package apigateway

import "github.com/stackshy/cloudemu/v2/services/apigateway/driver"

// createRestAPIRequest is the CreateRestApi request body (restJson1).
type createRestAPIRequest struct {
	Name                      string                 `json:"name"`
	Description               string                 `json:"description"`
	Version                   string                 `json:"version"`
	APIKeySource              string                 `json:"apiKeySource"`
	BinaryMediaTypes          []string               `json:"binaryMediaTypes"`
	Tags                      map[string]string      `json:"tags"`
	EndpointConfiguration     *endpointConfiguration `json:"endpointConfiguration"`
	DisableExecuteAPIEndpoint bool                   `json:"disableExecuteApiEndpoint"`
	MinimumCompressionSize    *int                   `json:"minimumCompressionSize"`
	Policy                    string                 `json:"policy"`
}

// patchRequest is the shared update request body: an AWS patchOperations
// (JSON Patch) document sent to any Update* operation.
type patchRequest struct {
	PatchOperations []patchOperation `json:"patchOperations"`
}

// patchOperation is one JSON Patch op. Value is decoded as a raw string because
// API Gateway always encodes patch values as strings, even for bool/int fields.
type patchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
	From  string `json:"from"`
}

// toPatchOps converts decoded wire patch ops to the driver's type.
func toPatchOps(in []patchOperation) []driver.PatchOperation {
	out := make([]driver.PatchOperation, 0, len(in))
	for _, op := range in {
		out = append(out, driver.PatchOperation{Op: op.Op, Path: op.Path, Value: op.Value, From: op.From})
	}

	return out
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

// putIntegrationRequest is the PutIntegration request body. The integration's
// backend method travels as "httpMethod" on the wire (the model's locationName
// for integrationHttpMethod).
type putIntegrationRequest struct {
	Type                  string `json:"type"`
	IntegrationHTTPMethod string `json:"httpMethod"`
	URI                   string `json:"uri"`
	PassthroughBehavior   string `json:"passthroughBehavior"`
	TimeoutInMillis       int    `json:"timeoutInMillis"`
}

// createDeploymentRequest is the CreateDeployment request body.
type createDeploymentRequest struct {
	StageName        string            `json:"stageName"`
	StageDescription string            `json:"stageDescription"`
	Description      string            `json:"description"`
	Variables        map[string]string `json:"variables"`
}

// createStageRequest is the CreateStage request body.
type createStageRequest struct {
	StageName            string            `json:"stageName"`
	DeploymentID         string            `json:"deploymentId"`
	Description          string            `json:"description"`
	Variables            map[string]string `json:"variables"`
	DocumentationVersion string            `json:"documentationVersion"`
}

// apiStatusAvailable is the RestApi apiStatus of a ready API.
const apiStatusAvailable = "AVAILABLE"

// restAPIResponse is the RestApi wire object.
type restAPIResponse struct {
	ID                        string                 `json:"id"`
	Name                      string                 `json:"name"`
	Description               string                 `json:"description,omitempty"`
	Version                   string                 `json:"version,omitempty"`
	CreatedDate               int64                  `json:"createdDate"`
	RootResourceID            string                 `json:"rootResourceId"`
	APIStatus                 string                 `json:"apiStatus"`
	APIKeySource              string                 `json:"apiKeySource,omitempty"`
	Tags                      map[string]string      `json:"tags,omitempty"`
	BinaryMediaTypes          []string               `json:"binaryMediaTypes,omitempty"`
	EndpointConfiguration     *endpointConfiguration `json:"endpointConfiguration,omitempty"`
	DisableExecuteAPIEndpoint bool                   `json:"disableExecuteApiEndpoint"`
	MinimumCompressionSize    *int                   `json:"minimumCompressionSize,omitempty"`
	Policy                    string                 `json:"policy,omitempty"`
}

// listRestAPIsResponse is the GetRestApis wire object.
type listRestAPIsResponse struct {
	Item []restAPIResponse `json:"item"`
}

// resourceResponse is the Resource wire object. ResourceMethods holds a full
// methodResponse per method under embed=methods, and an empty object per method
// otherwise.
type resourceResponse struct {
	ID              string         `json:"id"`
	ParentID        string         `json:"parentId,omitempty"`
	PathPart        string         `json:"pathPart,omitempty"`
	Path            string         `json:"path"`
	ResourceMethods map[string]any `json:"resourceMethods,omitempty"`
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
	TimeoutInMillis     int    `json:"timeoutInMillis,omitempty"`
}

// deploymentResponse is the Deployment wire object. APISummary is only sent
// for GetDeployment with embed=apisummary.
type deploymentResponse struct {
	ID          string                               `json:"id"`
	Description string                               `json:"description,omitempty"`
	CreatedDate int64                                `json:"createdDate"`
	APISummary  map[string]map[string]methodSnapshot `json:"apiSummary,omitempty"`
}

// methodSnapshot is one method's entry in a deployment's apiSummary.
type methodSnapshot struct {
	AuthorizationType string `json:"authorizationType,omitempty"`
	APIKeyRequired    bool   `json:"apiKeyRequired"`
}

// listDeploymentsResponse is the GetDeployments wire object.
type listDeploymentsResponse struct {
	Item []deploymentResponse `json:"item"`
}

// stageResponse is the Stage wire object.
type stageResponse struct {
	StageName            string            `json:"stageName"`
	DeploymentID         string            `json:"deploymentId,omitempty"`
	Description          string            `json:"description,omitempty"`
	CreatedDate          int64             `json:"createdDate"`
	Variables            map[string]string `json:"variables,omitempty"`
	ClientCertificateID  string            `json:"clientCertificateId,omitempty"`
	DocumentationVersion string            `json:"documentationVersion,omitempty"`
}

// listStagesResponse is the GetStages wire object.
type listStagesResponse struct {
	Item []stageResponse `json:"item"`
}

func toRestAPIResponse(a *driver.RestAPI) restAPIResponse {
	// An in-memory API is usable as soon as it exists, so apiStatus is always
	// AVAILABLE. The AWS provider waits on it before creating children.
	resp := restAPIResponse{
		ID: a.ID, Name: a.Name, Description: a.Description, Version: a.Version,
		CreatedDate: a.CreatedDate, RootResourceID: a.RootResourceID, APIStatus: apiStatusAvailable,
		APIKeySource: a.APIKeySource, Tags: a.Tags, BinaryMediaTypes: a.BinaryMediaTypes,
		DisableExecuteAPIEndpoint: a.DisableExecuteAPIEndpoint,
		MinimumCompressionSize:    a.MinimumCompressionSize, Policy: a.Policy,
	}
	if len(a.EndpointConfigurationTypes) > 0 {
		resp.EndpointConfiguration = &endpointConfiguration{Types: a.EndpointConfigurationTypes}
	}

	return resp
}

// toResourceResponse renders a resource. Its methods are listed by name with
// an empty object each, as API Gateway does unless embed=methods is requested.
func toResourceResponse(r *driver.Resource) resourceResponse {
	return renderResource(r, false)
}

// toEmbeddedResourceResponse renders a resource with each method's full Method
// object, the embed=methods form.
func toEmbeddedResourceResponse(r *driver.Resource) resourceResponse {
	return renderResource(r, true)
}

func renderResource(r *driver.Resource, embedMethods bool) resourceResponse {
	resp := resourceResponse{ID: r.ID, ParentID: r.ParentID, PathPart: r.PathPart, Path: r.Path}

	if len(r.Methods) > 0 {
		resp.ResourceMethods = make(map[string]any, len(r.Methods))

		for name, mth := range r.Methods {
			if embedMethods {
				resp.ResourceMethods[name] = toMethodResponse(mth)
			} else {
				resp.ResourceMethods[name] = struct{}{}
			}
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
		TimeoutInMillis: ig.TimeoutInMillis,
	}
}

func toDeploymentResponse(d *driver.Deployment) deploymentResponse {
	return deploymentResponse{ID: d.ID, Description: d.Description, CreatedDate: d.CreatedDate}
}

// toDeploymentSummaryResponse renders a deployment with its apiSummary, the
// embed=apisummary form.
func toDeploymentSummaryResponse(d *driver.Deployment) deploymentResponse {
	resp := toDeploymentResponse(d)
	resp.APISummary = make(map[string]map[string]methodSnapshot, len(d.APISummary))

	for path, methods := range d.APISummary {
		out := make(map[string]methodSnapshot, len(methods))
		for name, ms := range methods {
			out[name] = methodSnapshot{AuthorizationType: ms.AuthorizationType, APIKeyRequired: ms.APIKeyRequired}
		}

		resp.APISummary[path] = out
	}

	return resp
}

func toStageResponse(s *driver.Stage) stageResponse {
	return stageResponse{
		StageName: s.StageName, DeploymentID: s.DeploymentID, Description: s.Description,
		CreatedDate: s.CreatedDate, Variables: s.Variables,
		ClientCertificateID: s.ClientCertificateID, DocumentationVersion: s.DocumentationVersion,
	}
}
