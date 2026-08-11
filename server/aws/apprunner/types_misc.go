package apprunner

import "encoding/json"

// --- ObservabilityConfiguration shapes ---

type createObsRequest struct {
	ObservabilityConfigurationName string          `json:"ObservabilityConfigurationName"`
	TraceConfiguration             json.RawMessage `json:"TraceConfiguration"`
	Tags                           []tag           `json:"Tags"`
}

type obsArnRequest struct {
	ObservabilityConfigurationArn string `json:"ObservabilityConfigurationArn"`
}

type listObsRequest struct {
	ObservabilityConfigurationName string `json:"ObservabilityConfigurationName"`
	LatestOnly                     bool   `json:"LatestOnly"`
	NextToken                      string `json:"NextToken"`
	MaxResults                     int32  `json:"MaxResults"`
}

type wireObs struct {
	ObservabilityConfigurationArn      string          `json:"ObservabilityConfigurationArn"`
	ObservabilityConfigurationName     string          `json:"ObservabilityConfigurationName"`
	ObservabilityConfigurationRevision int32           `json:"ObservabilityConfigurationRevision"`
	Status                             string          `json:"Status"`
	Latest                             bool            `json:"Latest"`
	TraceConfiguration                 json.RawMessage `json:"TraceConfiguration,omitempty"`
	CreatedAt                          *float64        `json:"CreatedAt"`
	DeletedAt                          *float64        `json:"DeletedAt,omitempty"`
}

type obsResponse struct {
	ObservabilityConfiguration wireObs `json:"ObservabilityConfiguration"`
}

type obsSummaryItem struct {
	ObservabilityConfigurationArn      string `json:"ObservabilityConfigurationArn"`
	ObservabilityConfigurationName     string `json:"ObservabilityConfigurationName"`
	ObservabilityConfigurationRevision int32  `json:"ObservabilityConfigurationRevision"`
}

type listObsResponse struct {
	ObservabilityConfigurationSummaryList []obsSummaryItem `json:"ObservabilityConfigurationSummaryList"`
	NextToken                             string           `json:"NextToken,omitempty"`
}

// --- VpcConnector shapes ---

type createVpcConnectorRequest struct {
	VpcConnectorName string   `json:"VpcConnectorName"`
	Subnets          []string `json:"Subnets"`
	SecurityGroups   []string `json:"SecurityGroups"`
	Tags             []tag    `json:"Tags"`
}

type vpcConnectorArnRequest struct {
	VpcConnectorArn string `json:"VpcConnectorArn"`
}

type listVpcConnectorsRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type wireVpcConnector struct {
	VpcConnectorArn      string   `json:"VpcConnectorArn"`
	VpcConnectorName     string   `json:"VpcConnectorName"`
	VpcConnectorRevision int32    `json:"VpcConnectorRevision"`
	Status               string   `json:"Status"`
	Subnets              []string `json:"Subnets"`
	SecurityGroups       []string `json:"SecurityGroups"`
	CreatedAt            *float64 `json:"CreatedAt"`
	DeletedAt            *float64 `json:"DeletedAt,omitempty"`
}

type vpcConnectorResponse struct {
	VpcConnector wireVpcConnector `json:"VpcConnector"`
}

type listVpcConnectorsResponse struct {
	VpcConnectors []wireVpcConnector `json:"VpcConnectors"`
	NextToken     string             `json:"NextToken,omitempty"`
}

// --- VpcIngressConnection shapes ---

type createVpcIngressRequest struct {
	VpcIngressConnectionName string          `json:"VpcIngressConnectionName"`
	ServiceArn               string          `json:"ServiceArn"`
	IngressVpcConfiguration  json.RawMessage `json:"IngressVpcConfiguration"`
	Tags                     []tag           `json:"Tags"`
}

type vpcIngressArnRequest struct {
	VpcIngressConnectionArn string `json:"VpcIngressConnectionArn"`
}

type updateVpcIngressRequest struct {
	VpcIngressConnectionArn string          `json:"VpcIngressConnectionArn"`
	IngressVpcConfiguration json.RawMessage `json:"IngressVpcConfiguration"`
}

type listVpcIngressRequest struct {
	Filter     *vpcIngressFilter `json:"Filter"`
	NextToken  string            `json:"NextToken"`
	MaxResults int32             `json:"MaxResults"`
}

type vpcIngressFilter struct {
	ServiceArn    string `json:"ServiceArn"`
	VpcEndpointID string `json:"VpcEndpointId"`
}

type wireVpcIngress struct {
	VpcIngressConnectionArn  string          `json:"VpcIngressConnectionArn"`
	VpcIngressConnectionName string          `json:"VpcIngressConnectionName"`
	ServiceArn               string          `json:"ServiceArn"`
	Status                   string          `json:"Status"`
	AccountID                string          `json:"AccountId,omitempty"`
	DomainName               string          `json:"DomainName,omitempty"`
	IngressVpcConfiguration  json.RawMessage `json:"IngressVpcConfiguration,omitempty"`
	CreatedAt                *float64        `json:"CreatedAt"`
	DeletedAt                *float64        `json:"DeletedAt,omitempty"`
}

type vpcIngressResponse struct {
	VpcIngressConnection wireVpcIngress `json:"VpcIngressConnection"`
}

type vpcIngressSummaryItem struct {
	VpcIngressConnectionArn string `json:"VpcIngressConnectionArn"`
	ServiceArn              string `json:"ServiceArn"`
}

type listVpcIngressResponse struct {
	VpcIngressConnectionSummaryList []vpcIngressSummaryItem `json:"VpcIngressConnectionSummaryList"`
	NextToken                       string                  `json:"NextToken,omitempty"`
}

// --- CustomDomain shapes ---

type associateCustomDomainRequest struct {
	ServiceArn         string `json:"ServiceArn"`
	DomainName         string `json:"DomainName"`
	EnableWWWSubdomain *bool  `json:"EnableWWWSubdomain"`
}

type disassociateCustomDomainRequest struct {
	ServiceArn string `json:"ServiceArn"`
	DomainName string `json:"DomainName"`
}

type describeCustomDomainsRequest struct {
	ServiceArn string `json:"ServiceArn"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type wireCertRecord struct {
	Name   string `json:"Name,omitempty"`
	Type   string `json:"Type,omitempty"`
	Value  string `json:"Value,omitempty"`
	Status string `json:"Status,omitempty"`
}

type wireCustomDomain struct {
	DomainName                   string           `json:"DomainName"`
	EnableWWWSubdomain           bool             `json:"EnableWWWSubdomain"`
	Status                       string           `json:"Status"`
	CertificateValidationRecords []wireCertRecord `json:"CertificateValidationRecords,omitempty"`
}

type associateCustomDomainResponse struct {
	CustomDomain  wireCustomDomain `json:"CustomDomain"`
	DNSTarget     string           `json:"DNSTarget"`
	ServiceArn    string           `json:"ServiceArn"`
	VpcDNSTargets []any            `json:"VpcDNSTargets"`
}

type describeCustomDomainsResponse struct {
	CustomDomains []wireCustomDomain `json:"CustomDomains"`
	DNSTarget     string             `json:"DNSTarget"`
	ServiceArn    string             `json:"ServiceArn"`
	VpcDNSTargets []any              `json:"VpcDNSTargets"`
	NextToken     string             `json:"NextToken,omitempty"`
}

// --- Operations shapes ---

type listOperationsRequest struct {
	ServiceArn string `json:"ServiceArn"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type wireOperation struct {
	ID        string   `json:"Id"`
	Type      string   `json:"Type"`
	Status    string   `json:"Status"`
	TargetArn string   `json:"TargetArn,omitempty"`
	StartedAt *float64 `json:"StartedAt"`
	EndedAt   *float64 `json:"EndedAt,omitempty"`
	UpdatedAt *float64 `json:"UpdatedAt,omitempty"`
}

type listOperationsResponse struct {
	OperationSummaryList []wireOperation `json:"OperationSummaryList"`
	NextToken            string          `json:"NextToken,omitempty"`
}

// --- Tags shapes ---

type tagResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []tag  `json:"Tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

type listTagsRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type listTagsResponse struct {
	Tags []tag `json:"Tags"`
}
