package apigateway

import (
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// --- API keys and usage plans ---

type stageKeyJSON struct {
	RestAPIID string `json:"restApiId"`
	StageName string `json:"stageName"`
}

type createAPIKeyRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Value       string            `json:"value"`
	CustomerID  string            `json:"customerId"`
	StageKeys   []stageKeyJSON    `json:"stageKeys"`
	Tags        map[string]string `json:"tags"`
}

type apiKeyResponse struct {
	ID              string            `json:"id"`
	Value           string            `json:"value,omitempty"`
	Name            string            `json:"name,omitempty"`
	CustomerID      string            `json:"customerId,omitempty"`
	Description     string            `json:"description,omitempty"`
	Enabled         bool              `json:"enabled"`
	CreatedDate     int64             `json:"createdDate"`
	LastUpdatedDate int64             `json:"lastUpdatedDate"`
	StageKeys       []string          `json:"stageKeys,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
}

func toAPIKeyResponse(k *driver.APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID, Value: k.Value, Name: k.Name, CustomerID: k.CustomerID, Description: k.Description, Enabled: k.Enabled,
		CreatedDate: k.CreatedDate, LastUpdatedDate: k.LastUpdatedDate, StageKeys: k.StageKeys, Tags: k.Tags,
	}
}

type throttleJSON struct {
	BurstLimit int     `json:"burstLimit"`
	RateLimit  float64 `json:"rateLimit"`
}

type quotaJSON struct {
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Period string `json:"period"`
}

type usagePlanStageJSON struct {
	APIID    string                  `json:"apiId"`
	Stage    string                  `json:"stage"`
	Throttle map[string]throttleJSON `json:"throttle,omitempty"`
}

type createUsagePlanRequest struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	APIStages   []usagePlanStageJSON `json:"apiStages"`
	Throttle    *throttleJSON        `json:"throttle"`
	Quota       *quotaJSON           `json:"quota"`
	Tags        map[string]string    `json:"tags"`
}

type usagePlanResponse struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	APIStages   []usagePlanStageJSON `json:"apiStages,omitempty"`
	Throttle    *throttleJSON        `json:"throttle,omitempty"`
	Quota       *quotaJSON           `json:"quota,omitempty"`
	ProductCode string               `json:"productCode,omitempty"`
	Tags        map[string]string    `json:"tags,omitempty"`
}

func planStagesFromWire(in []usagePlanStageJSON) []driver.UsagePlanStage {
	out := make([]driver.UsagePlanStage, len(in))

	for i, s := range in {
		out[i] = driver.UsagePlanStage{RestAPIID: s.APIID, Stage: s.Stage}

		if s.Throttle != nil {
			out[i].Throttle = make(map[string]driver.ThrottleSettings, len(s.Throttle))
			for k, v := range s.Throttle {
				out[i].Throttle[k] = driver.ThrottleSettings{BurstLimit: v.BurstLimit, RateLimit: v.RateLimit}
			}
		}
	}

	return out
}

func toUsagePlanResponse(p *driver.UsagePlan) usagePlanResponse {
	out := usagePlanResponse{
		ID: p.ID, Name: p.Name, Description: p.Description, ProductCode: p.ProductCode, Tags: p.Tags,
	}

	for _, s := range p.APIStages {
		ws := usagePlanStageJSON{APIID: s.RestAPIID, Stage: s.Stage}

		if len(s.Throttle) > 0 {
			ws.Throttle = make(map[string]throttleJSON, len(s.Throttle))
			for k, v := range s.Throttle {
				ws.Throttle[k] = throttleJSON{BurstLimit: v.BurstLimit, RateLimit: v.RateLimit}
			}
		}

		out.APIStages = append(out.APIStages, ws)
	}

	if p.Throttle != nil {
		out.Throttle = &throttleJSON{BurstLimit: p.Throttle.BurstLimit, RateLimit: p.Throttle.RateLimit}
	}

	if p.Quota != nil {
		out.Quota = &quotaJSON{Limit: p.Quota.Limit, Offset: p.Quota.Offset, Period: p.Quota.Period}
	}

	return out
}

type createUsagePlanKeyRequest struct {
	KeyID   string `json:"keyId"`
	KeyType string `json:"keyType"`
}

type usagePlanKeyResponse struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
	Name  string `json:"name,omitempty"`
}

type usageResponse struct {
	UsagePlanID string               `json:"usagePlanId"`
	StartDate   string               `json:"startDate"`
	EndDate     string               `json:"endDate"`
	Items       map[string][][]int64 `json:"values"`
	Position    string               `json:"position,omitempty"`
}

func toUsageResponse(u *driver.Usage) usageResponse {
	out := usageResponse{
		UsagePlanID: u.UsagePlanID, StartDate: u.StartDate, EndDate: u.EndDate, Position: u.Position,
		Items: make(map[string][][]int64, len(u.Items)),
	}

	for _, it := range u.Items {
		days := make([][]int64, len(it.Days))
		for i, d := range it.Days {
			days[i] = []int64{d[0], d[1]}
		}

		out.Items[it.KeyID] = days
	}

	return out
}

// --- authorizers, models, validators, gateway responses ---

type authorizerRequest struct {
	Name                         string   `json:"name"`
	Type                         string   `json:"type"`
	ProviderARNs                 []string `json:"providerARNs"`
	AuthType                     string   `json:"authType"`
	AuthorizerURI                string   `json:"authorizerUri"`
	AuthorizerCredentials        string   `json:"authorizerCredentials"`
	IdentitySource               string   `json:"identitySource"`
	IdentityValidationExpression string   `json:"identityValidationExpression"`
	AuthorizerResultTTLInSeconds *int     `json:"authorizerResultTtlInSeconds"`
}

type authorizerResponse struct {
	ID                           string   `json:"id"`
	Name                         string   `json:"name"`
	Type                         string   `json:"type"`
	ProviderARNs                 []string `json:"providerARNs,omitempty"`
	AuthType                     string   `json:"authType,omitempty"`
	AuthorizerURI                string   `json:"authorizerUri,omitempty"`
	AuthorizerCredentials        string   `json:"authorizerCredentials,omitempty"`
	IdentitySource               string   `json:"identitySource,omitempty"`
	IdentityValidationExpression string   `json:"identityValidationExpression,omitempty"`
	AuthorizerResultTTLInSeconds *int     `json:"authorizerResultTtlInSeconds,omitempty"`
}

func toAuthorizerResponse(a *driver.Authorizer) authorizerResponse {
	return authorizerResponse{
		ID: a.ID, Name: a.Name, Type: a.Type, ProviderARNs: a.ProviderARNs, AuthType: a.AuthType,
		AuthorizerURI: a.AuthorizerURI, AuthorizerCredentials: a.AuthorizerCredentials, IdentitySource: a.IdentitySource,
		IdentityValidationExpression: a.IdentityValidationExpression, AuthorizerResultTTLInSeconds: a.AuthorizerResultTTLInSeconds,
	}
}

type modelRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Schema      string `json:"schema"`
	ContentType string `json:"contentType"`
}

type modelResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schema      string `json:"schema,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

func toModelResponse(m *driver.Model) modelResponse {
	return modelResponse{ID: m.ID, Name: m.Name, Description: m.Description, Schema: m.Schema, ContentType: m.ContentType}
}

type requestValidatorRequest struct {
	Name                      string `json:"name"`
	ValidateRequestBody       bool   `json:"validateRequestBody"`
	ValidateRequestParameters bool   `json:"validateRequestParameters"`
}

type requestValidatorResponse struct {
	ID                        string `json:"id"`
	Name                      string `json:"name"`
	ValidateRequestBody       bool   `json:"validateRequestBody"`
	ValidateRequestParameters bool   `json:"validateRequestParameters"`
}

func toRequestValidatorResponse(v *driver.RequestValidator) requestValidatorResponse {
	return requestValidatorResponse{
		ID: v.ID, Name: v.Name, ValidateRequestBody: v.ValidateRequestBody, ValidateRequestParameters: v.ValidateRequestParameters,
	}
}

type gatewayResponseRequest struct {
	StatusCode         string            `json:"statusCode"`
	ResponseParameters map[string]string `json:"responseParameters"`
	ResponseTemplates  map[string]string `json:"responseTemplates"`
}

type gatewayResponseResponse struct {
	ResponseType       string            `json:"responseType"`
	StatusCode         string            `json:"statusCode,omitempty"`
	ResponseParameters map[string]string `json:"responseParameters,omitempty"`
	ResponseTemplates  map[string]string `json:"responseTemplates,omitempty"`
	DefaultResponse    bool              `json:"defaultResponse"`
}

func toGatewayResponseResponse(g *driver.GatewayResponse) gatewayResponseResponse {
	return gatewayResponseResponse{
		ResponseType: g.ResponseType, StatusCode: g.StatusCode, ResponseParameters: g.ResponseParameters,
		ResponseTemplates: g.ResponseTemplates, DefaultResponse: g.DefaultResponse,
	}
}

type testInvokeMethodRequest struct {
	PathWithQueryString string              `json:"pathWithQueryString"`
	Body                string              `json:"body"`
	Headers             map[string]string   `json:"headers"`
	MultiValueHeaders   map[string][]string `json:"multiValueHeaders"`
	ClientCertificateID string              `json:"clientCertificateId"`
	StageVariables      map[string]string   `json:"stageVariables"`
}

type testInvokeMethodResponse struct {
	Status            int                 `json:"status"`
	Body              string              `json:"body"`
	Headers           map[string]string   `json:"headers,omitempty"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders,omitempty"`
	Log               string              `json:"log"`
	Latency           int64               `json:"latency"`
}

type testInvokeAuthorizerRequest struct {
	Headers             map[string]string   `json:"headers"`
	MultiValueHeaders   map[string][]string `json:"multiValueHeaders"`
	PathWithQueryString string              `json:"pathWithQueryString"`
	Body                string              `json:"body"`
	StageVariables      map[string]string   `json:"stageVariables"`
	AdditionalContext   map[string]string   `json:"additionalContext"`
}

type testInvokeAuthorizerResponse struct {
	ClientStatus  int                 `json:"clientStatus"`
	Log           string              `json:"log"`
	Latency       int64               `json:"latency"`
	Policy        string              `json:"policy,omitempty"`
	PrincipalID   string              `json:"principalId,omitempty"`
	Authorization map[string][]string `json:"authorization,omitempty"`
	Claims        map[string]string   `json:"claims,omitempty"`
}

// --- domain names and VPC links ---

type domainNameRequest struct {
	DomainName              string                 `json:"domainName"`
	CertificateName         string                 `json:"certificateName"`
	CertificateARN          string                 `json:"certificateArn"`
	RegionalCertificateName string                 `json:"regionalCertificateName"`
	RegionalCertificateARN  string                 `json:"regionalCertificateArn"`
	EndpointConfiguration   *endpointConfiguration `json:"endpointConfiguration"`
	SecurityPolicy          string                 `json:"securityPolicy"`
	Tags                    map[string]string      `json:"tags"`
}

type domainNameResponse struct {
	DomainName               string                 `json:"domainName"`
	CertificateName          string                 `json:"certificateName,omitempty"`
	CertificateARN           string                 `json:"certificateArn,omitempty"`
	CertificateUploadDate    int64                  `json:"certificateUploadDate,omitempty"`
	RegionalDomainName       string                 `json:"regionalDomainName,omitempty"`
	RegionalHostedZoneID     string                 `json:"regionalHostedZoneId,omitempty"`
	RegionalCertificateName  string                 `json:"regionalCertificateName,omitempty"`
	RegionalCertificateARN   string                 `json:"regionalCertificateArn,omitempty"`
	DistributionDomainName   string                 `json:"distributionDomainName,omitempty"`
	DistributionHostedZoneID string                 `json:"distributionHostedZoneId,omitempty"`
	EndpointConfiguration    *endpointConfiguration `json:"endpointConfiguration,omitempty"`
	DomainNameStatus         string                 `json:"domainNameStatus"`
	DomainNameStatusMessage  string                 `json:"domainNameStatusMessage,omitempty"`
	SecurityPolicy           string                 `json:"securityPolicy,omitempty"`
	Tags                     map[string]string      `json:"tags,omitempty"`
}

func toDomainNameResponse(d *driver.DomainName) domainNameResponse {
	return domainNameResponse{
		DomainName: d.DomainName, CertificateName: d.CertificateName, CertificateARN: d.CertificateARN,
		CertificateUploadDate: d.CertificateUploadDate, RegionalDomainName: d.RegionalDomainName,
		RegionalHostedZoneID: d.RegionalHostedZoneID, RegionalCertificateName: d.RegionalCertificateName,
		RegionalCertificateARN: d.RegionalCertificateARN, DistributionDomainName: d.DistributionDomainName,
		DistributionHostedZoneID: d.DistributionHostedZoneID, DomainNameStatus: d.DomainNameStatus,
		DomainNameStatusMessage: d.DomainNameStatusMessage, SecurityPolicy: d.SecurityPolicy, Tags: d.Tags,
		EndpointConfiguration: &endpointConfiguration{Types: d.EndpointConfigurationType},
	}
}

type basePathMappingRequest struct {
	BasePath  string `json:"basePath"`
	RestAPIID string `json:"restApiId"`
	Stage     string `json:"stage"`
}

type basePathMappingResponse struct {
	BasePath  string `json:"basePath"`
	RestAPIID string `json:"restApiId"`
	Stage     string `json:"stage,omitempty"`
}

func toBasePathMappingResponse(b *driver.BasePathMapping) basePathMappingResponse {
	return basePathMappingResponse{BasePath: b.BasePath, RestAPIID: b.RestAPIID, Stage: b.Stage}
}

type vpcLinkRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	TargetARNs  []string          `json:"targetArns"`
	Tags        map[string]string `json:"tags"`
}

type vpcLinkResponse struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	TargetARNs    []string          `json:"targetArns"`
	Status        string            `json:"status"`
	StatusMessage string            `json:"statusMessage,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
}

func toVpcLinkResponse(l *driver.VpcLink) vpcLinkResponse {
	return vpcLinkResponse{
		ID: l.ID, Name: l.Name, Description: l.Description, TargetARNs: l.TargetARNs, Status: l.Status,
		StatusMessage: l.StatusMessage, Tags: l.Tags,
	}
}

// listResponse is the shared {item, position} body of every paged list.
type listResponse[R any] struct {
	Position string `json:"position,omitempty"`
	Item     []R    `json:"item"`
}
