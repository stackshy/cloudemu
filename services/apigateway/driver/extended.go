package driver

import "context"

// Authorizer types.
const (
	AuthorizerToken   = "TOKEN"
	AuthorizerRequest = "REQUEST"
	AuthorizerCognito = "COGNITO_USER_POOLS"
)

// Usage plan quota periods.
const (
	QuotaDay   = "DAY"
	QuotaWeek  = "WEEK"
	QuotaMonth = "MONTH"
)

// StageKey names a stage an API key is enabled for.
type StageKey struct {
	RestAPIID string
	StageName string
}

// APIKey is an API Gateway API key. Value is the secret the caller sends in the
// x-api-key header; list calls omit it unless the caller asks for values.
type APIKey struct {
	ID              string
	Value           string
	Name            string
	CustomerID      string
	Description     string
	Enabled         bool
	CreatedDate     int64
	LastUpdatedDate int64
	StageKeys       []string // "restApiId/stage"
	Tags            map[string]string
}

// CreateAPIKeyInput carries the fields CreateApiKey accepts.
type CreateAPIKeyInput struct {
	Name        string
	Description string
	Enabled     bool
	Value       string
	CustomerID  string
	StageKeys   []StageKey
	Tags        map[string]string
}

// GetAPIKeysInput carries the GetApiKeys filters.
type GetAPIKeysInput struct {
	NameQuery     string
	CustomerID    string
	IncludeValues bool
	PageInput
}

// ThrottleSettings is declared in driver.go (account level); a usage plan
// throttle and a per-method throttle reuse it.

// QuotaSettings caps the number of requests per Period; Offset is the number of
// requests already counted in the first period.
type QuotaSettings struct {
	Limit  int
	Offset int
	Period string
}

// UsagePlanStage is an API stage a usage plan applies to, with optional
// per-method throttles keyed "resourcePath/HTTPMETHOD".
type UsagePlanStage struct {
	RestAPIID string
	Stage     string
	Throttle  map[string]ThrottleSettings
}

// UsagePlan bundles throttle and quota limits for the API keys attached to it.
type UsagePlan struct {
	ID          string
	Name        string
	Description string
	APIStages   []UsagePlanStage
	Throttle    *ThrottleSettings
	Quota       *QuotaSettings
	ProductCode string
	Tags        map[string]string
}

// CreateUsagePlanInput carries the fields CreateUsagePlan accepts.
type CreateUsagePlanInput struct {
	Name        string
	Description string
	APIStages   []UsagePlanStage
	Throttle    *ThrottleSettings
	Quota       *QuotaSettings
	Tags        map[string]string
}

// UsagePlanKey is an API key attached to a usage plan.
type UsagePlanKey struct {
	ID    string
	Type  string
	Value string
	Name  string
}

// UsageItem is one day's usage of one key: requests used and remaining.
type UsageItem struct {
	KeyID string
	Days  [][2]int64 // per day from StartDate: {used, remaining}
}

// Usage is the GetUsage result.
type Usage struct {
	UsagePlanID string
	StartDate   string
	EndDate     string
	Items       []UsageItem
	Position    string
}

// GetUsageInput carries the GetUsage request.
type GetUsageInput struct {
	UsagePlanID string
	KeyID       string
	StartDate   string // YYYY-MM-DD
	EndDate     string // YYYY-MM-DD
	PageInput
}

// Page is one page of a paged collection; Position is empty on the last page.
type Page[T any] struct {
	Items    []T
	Position string
}

// APIKeyPage is one page of APIKeys.
type APIKeyPage = Page[APIKey]

// UsagePlanPage is one page of UsagePlans.
type UsagePlanPage = Page[UsagePlan]

// UsagePlanKeyPage is one page of UsagePlanKeys.
type UsagePlanKeyPage = Page[UsagePlanKey]

// AuthorizerPage is one page of Authorizers.
type AuthorizerPage = Page[Authorizer]

// ModelPage is one page of Models.
type ModelPage = Page[Model]

// RequestValidatorPage is one page of RequestValidators.
type RequestValidatorPage = Page[RequestValidator]

// GatewayResponsePage is one page of GatewayResponses.
type GatewayResponsePage = Page[GatewayResponse]

// DomainNamePage is one page of DomainNames.
type DomainNamePage = Page[DomainName]

// BasePathMappingPage is one page of BasePathMappings.
type BasePathMappingPage = Page[BasePathMapping]

// VpcLinkPage is one page of VpcLinks.
type VpcLinkPage = Page[VpcLink]

// APIKeys is the optional API key capability.
type APIKeys interface {
	CreateAPIKey(ctx context.Context, in *CreateAPIKeyInput) (*APIKey, error)
	GetAPIKey(ctx context.Context, id string, includeValue bool) (*APIKey, error)
	GetAPIKeys(ctx context.Context, in *GetAPIKeysInput) (*APIKeyPage, error)
	UpdateAPIKey(ctx context.Context, id string, ops []PatchOperation) (*APIKey, error)
	DeleteAPIKey(ctx context.Context, id string) error
}

// UsagePlans is the optional usage plan capability (plans, their keys and usage).
type UsagePlans interface {
	CreateUsagePlan(ctx context.Context, in *CreateUsagePlanInput) (*UsagePlan, error)
	GetUsagePlan(ctx context.Context, id string) (*UsagePlan, error)
	GetUsagePlans(ctx context.Context, keyID string, page PageInput) (*UsagePlanPage, error)
	UpdateUsagePlan(ctx context.Context, id string, ops []PatchOperation) (*UsagePlan, error)
	DeleteUsagePlan(ctx context.Context, id string) error

	CreateUsagePlanKey(ctx context.Context, planID, keyID, keyType string) (*UsagePlanKey, error)
	GetUsagePlanKey(ctx context.Context, planID, keyID string) (*UsagePlanKey, error)
	GetUsagePlanKeys(ctx context.Context, planID, nameQuery string, page PageInput) (*UsagePlanKeyPage, error)
	DeleteUsagePlanKey(ctx context.Context, planID, keyID string) error

	GetUsage(ctx context.Context, in *GetUsageInput) (*Usage, error)
}

// Authorizer is a Lambda or Cognito authorizer of a REST API.
type Authorizer struct {
	ID                           string
	Name                         string
	Type                         string
	ProviderARNs                 []string
	AuthType                     string
	AuthorizerURI                string
	AuthorizerCredentials        string
	IdentitySource               string
	IdentityValidationExpression string
	AuthorizerResultTTLInSeconds *int
}

// CreateAuthorizerInput carries the fields CreateAuthorizer accepts.
type CreateAuthorizerInput struct {
	Name                         string
	Type                         string
	ProviderARNs                 []string
	AuthType                     string
	AuthorizerURI                string
	AuthorizerCredentials        string
	IdentitySource               string
	IdentityValidationExpression string
	AuthorizerResultTTLInSeconds *int
}

// Authorizers is the optional authorizer capability.
//
//nolint:dupl // each capability repeats the same CRUD method shape
type Authorizers interface {
	CreateAuthorizer(ctx context.Context, restAPIID string, in *CreateAuthorizerInput) (*Authorizer, error)
	GetAuthorizer(ctx context.Context, restAPIID, id string) (*Authorizer, error)
	GetAuthorizers(ctx context.Context, restAPIID string, page PageInput) (*AuthorizerPage, error)
	UpdateAuthorizer(ctx context.Context, restAPIID, id string, ops []PatchOperation) (*Authorizer, error)
	DeleteAuthorizer(ctx context.Context, restAPIID, id string) error
}

// Model is a JSON Schema model of a REST API.
type Model struct {
	ID          string
	Name        string
	Description string
	Schema      string
	ContentType string
}

// CreateModelInput carries the fields CreateModel accepts.
type CreateModelInput struct {
	Name        string
	Description string
	Schema      string
	ContentType string
}

// RequestValidator validates the parameters and/or body of a request before it
// reaches the integration.
type RequestValidator struct {
	ID                        string
	Name                      string
	ValidateRequestBody       bool
	ValidateRequestParameters bool
}

// CreateRequestValidatorInput carries the fields CreateRequestValidator accepts.
type CreateRequestValidatorInput struct {
	Name                      string
	ValidateRequestBody       bool
	ValidateRequestParameters bool
}

// Models is the optional model capability.
//
//nolint:dupl // each capability repeats the same CRUD method shape
type Models interface {
	CreateModel(ctx context.Context, restAPIID string, in *CreateModelInput) (*Model, error)
	GetModel(ctx context.Context, restAPIID, name string) (*Model, error)
	GetModels(ctx context.Context, restAPIID string, page PageInput) (*ModelPage, error)
	UpdateModel(ctx context.Context, restAPIID, name string, ops []PatchOperation) (*Model, error)
	DeleteModel(ctx context.Context, restAPIID, name string) error
}

// RequestValidators is the optional request validator capability.
//
//nolint:dupl // each capability repeats the same CRUD method shape
type RequestValidators interface {
	CreateRequestValidator(ctx context.Context, restAPIID string, in *CreateRequestValidatorInput) (*RequestValidator, error)
	GetRequestValidator(ctx context.Context, restAPIID, id string) (*RequestValidator, error)
	GetRequestValidators(ctx context.Context, restAPIID string, page PageInput) (*RequestValidatorPage, error)
	UpdateRequestValidator(ctx context.Context, restAPIID, id string, ops []PatchOperation) (*RequestValidator, error)
	DeleteRequestValidator(ctx context.Context, restAPIID, id string) error
}

// GatewayResponse is the response API Gateway returns for an error of one
// response type (for example DEFAULT_4XX or MISSING_AUTHENTICATION_TOKEN).
type GatewayResponse struct {
	ResponseType       string
	StatusCode         string
	ResponseParameters map[string]string
	ResponseTemplates  map[string]string
	DefaultResponse    bool
}

// PutGatewayResponseInput carries the fields PutGatewayResponse accepts.
type PutGatewayResponseInput struct {
	StatusCode         string
	ResponseParameters map[string]string
	ResponseTemplates  map[string]string
}

// GatewayResponses is the optional gateway response capability.
type GatewayResponses interface {
	PutGatewayResponse(ctx context.Context, restAPIID, responseType string, in *PutGatewayResponseInput) (*GatewayResponse, error)
	GetGatewayResponse(ctx context.Context, restAPIID, responseType string) (*GatewayResponse, error)
	GetGatewayResponses(ctx context.Context, restAPIID string, page PageInput) (*GatewayResponsePage, error)
	UpdateGatewayResponse(ctx context.Context, restAPIID, responseType string, ops []PatchOperation) (*GatewayResponse, error)
	// DeleteGatewayResponse restores the default for the response type.
	DeleteGatewayResponse(ctx context.Context, restAPIID, responseType string) error
}

// TestInvokeMethodInput carries the TestInvokeMethod request.
type TestInvokeMethodInput struct {
	RestAPIID           string
	ResourceID          string
	HTTPMethod          string
	PathWithQuery       string
	Body                string
	Headers             map[string]string
	MultiValueHeaders   map[string][]string
	StageVariables      map[string]string
	ClientCertificateID string
}

// TestInvokeMethodOutput is the TestInvokeMethod result.
type TestInvokeMethodOutput struct {
	Status            int
	Body              string
	Headers           map[string]string
	MultiValueHeaders map[string][]string
	Log               string
	LatencyMillis     int64
}

// TestInvokeAuthorizerInput carries the TestInvokeAuthorizer request.
type TestInvokeAuthorizerInput struct {
	RestAPIID         string
	AuthorizerID      string
	Headers           map[string]string
	MultiValueHeaders map[string][]string
	PathWithQuery     string
	Body              string
	StageVariables    map[string]string
	AdditionalContext map[string]string
}

// TestInvokeAuthorizerOutput is the TestInvokeAuthorizer result.
type TestInvokeAuthorizerOutput struct {
	ClientStatus  int
	Log           string
	LatencyMillis int64
	Policy        string
	PrincipalID   string
	Authorization map[string][]string
	Claims        map[string]string
}

// TestInvoker is the optional capability that runs a method or authorizer
// without deploying.
type TestInvoker interface {
	TestInvokeMethod(ctx context.Context, in *TestInvokeMethodInput) (*TestInvokeMethodOutput, error)
	TestInvokeAuthorizer(ctx context.Context, in *TestInvokeAuthorizerInput) (*TestInvokeAuthorizerOutput, error)
}

// Import / export modes.
const (
	ImportMerge     = "merge"
	ImportOverwrite = "overwrite"
)

// Export types and extensions.
const (
	ExportSwagger = "swagger"
	ExportOAS30   = "oas30"
)

// ImportRestAPIInput carries ImportRestApi: an OpenAPI 2.0 or 3.0 document (JSON
// or YAML) plus the import parameters.
type ImportRestAPIInput struct {
	Body           []byte
	FailOnWarnings bool
	Parameters     map[string]string
}

// PutRestAPIInput carries PutRestApi (merge or overwrite of an existing API).
type PutRestAPIInput struct {
	Mode           string
	Body           []byte
	FailOnWarnings bool
	Parameters     map[string]string
}

// ImportResult is the API an import produced and its warnings.
type ImportResult struct {
	API      *RestAPI
	Warnings []string
}

// GetExportInput carries GetExport.
type GetExportInput struct {
	RestAPIID  string
	StageName  string
	ExportType string
	Accept     string // application/json (default) or application/yaml
	Extensions []string
}

// Export is the exported document.
type Export struct {
	ContentType string
	Body        []byte
}

// OpenAPI is the optional OpenAPI import/export capability.
type OpenAPI interface {
	ImportRestAPI(ctx context.Context, in *ImportRestAPIInput) (*ImportResult, error)
	PutRestAPI(ctx context.Context, restAPIID string, in *PutRestAPIInput) (*ImportResult, error)
	GetExport(ctx context.Context, in *GetExportInput) (*Export, error)
}

// DomainName is a custom domain name of the account.
type DomainName struct {
	DomainName                string
	CertificateName           string
	CertificateARN            string
	CertificateUploadDate     int64
	RegionalDomainName        string
	RegionalHostedZoneID      string
	RegionalCertificateName   string
	RegionalCertificateARN    string
	DistributionDomainName    string
	DistributionHostedZoneID  string
	EndpointConfigurationType []string
	DomainNameStatus          string
	DomainNameStatusMessage   string
	SecurityPolicy            string
	Tags                      map[string]string
}

// CreateDomainNameInput carries the fields CreateDomainName accepts.
type CreateDomainNameInput struct {
	DomainName                string
	CertificateName           string
	CertificateARN            string
	RegionalCertificateName   string
	RegionalCertificateARN    string
	EndpointConfigurationType []string
	SecurityPolicy            string
	Tags                      map[string]string
}

// BasePathMapping maps a base path of a custom domain to an API stage.
type BasePathMapping struct {
	BasePath  string // "(none)" for the empty path
	RestAPIID string
	Stage     string
}

// DomainNames is the optional custom domain capability (domain names and their
// base path mappings).
type DomainNames interface {
	CreateDomainName(ctx context.Context, in *CreateDomainNameInput) (*DomainName, error)
	GetDomainName(ctx context.Context, name string) (*DomainName, error)
	GetDomainNames(ctx context.Context, page PageInput) (*DomainNamePage, error)
	UpdateDomainName(ctx context.Context, name string, ops []PatchOperation) (*DomainName, error)
	DeleteDomainName(ctx context.Context, name string) error

	CreateBasePathMapping(ctx context.Context, domain string, in BasePathMapping) (*BasePathMapping, error)
	GetBasePathMapping(ctx context.Context, domain, basePath string) (*BasePathMapping, error)
	GetBasePathMappings(ctx context.Context, domain string, page PageInput) (*BasePathMappingPage, error)
	UpdateBasePathMapping(ctx context.Context, domain, basePath string, ops []PatchOperation) (*BasePathMapping, error)
	DeleteBasePathMapping(ctx context.Context, domain, basePath string) error
}

// DomainResolver is the optional data-plane hook that maps a request addressed
// to a registered custom domain to the API and stage its base path mapping
// names. ok is false when host is not a registered domain or no mapping matches.
type DomainResolver interface {
	ResolveDomain(host, path string) (restAPIID, stage, rest string, ok bool)
}

// VpcLink is a VPC link to Network Load Balancers.
type VpcLink struct {
	ID            string
	Name          string
	Description   string
	TargetARNs    []string
	Status        string
	StatusMessage string
	Tags          map[string]string
}

// CreateVpcLinkInput carries the fields CreateVpcLink accepts.
type CreateVpcLinkInput struct {
	Name        string
	Description string
	TargetARNs  []string
	Tags        map[string]string
}

// VpcLinks is the optional VPC link capability.
type VpcLinks interface {
	CreateVpcLink(ctx context.Context, in *CreateVpcLinkInput) (*VpcLink, error)
	GetVpcLink(ctx context.Context, id string) (*VpcLink, error)
	GetVpcLinks(ctx context.Context, page PageInput) (*VpcLinkPage, error)
	UpdateVpcLink(ctx context.Context, id string, ops []PatchOperation) (*VpcLink, error)
	DeleteVpcLink(ctx context.Context, id string) error
}
