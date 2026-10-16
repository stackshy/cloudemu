package driver

import (
	"context"
	"encoding/json"
	"time"
)

// The interfaces below are optional capability groups. A driver implements the
// ones it supports; the wire handler finds them by type assertion and answers
// UnsupportedOperationException for a group the driver lacks, so the base
// NetworkFirewall interface is not widened for every implementer.

// FirewallSettings covers the firewall-level setters.
type FirewallSettings interface {
	UpdateFirewallDescription(ctx context.Context, firewallName, description, updateToken string) (*Firewall, error)
	UpdateSubnetChangeProtection(ctx context.Context, firewallName string, enabled bool, updateToken string) (*Firewall, error)
	UpdateFirewallPolicyChangeProtection(ctx context.Context, firewallName string, enabled bool, updateToken string) (*Firewall, error)
	UpdateAvailabilityZoneChangeProtection(ctx context.Context, firewallName string, enabled bool, updateToken string) (*Firewall, error)
	UpdateFirewallAnalysisSettings(ctx context.Context, firewallName string, analysisTypes []string, updateToken string) (*Firewall, error)
}

// AvailabilityZones covers the Availability-Zone associations of a transit
// gateway-attached firewall.
type AvailabilityZones interface {
	AssociateAvailabilityZones(ctx context.Context, firewallName string, zones []string, updateToken string) (*Firewall, error)
	DisassociateAvailabilityZones(ctx context.Context, firewallName string, zones []string, updateToken string) (*Firewall, error)
}

// ResourcePolicies covers the resource-sharing IAM policies attached to a rule
// group, firewall policy or firewall.
type ResourcePolicies interface {
	PutResourcePolicy(ctx context.Context, resourceARN, policy string) error
	DescribeResourcePolicy(ctx context.Context, resourceARN string) (string, error)
	DeleteResourcePolicy(ctx context.Context, resourceARN string) error
}

// TLSInspectionConfig is a TLS inspection configuration a firewall policy can
// reference. Configuration holds the "TLSInspectionConfiguration" payload
// (ServerCertificateConfigurations) exactly as the caller provided it.
type TLSInspectionConfig struct {
	Name                 string
	ARN                  string
	ID                   string
	Description          string
	Configuration        json.RawMessage
	CertificateARNs      []string
	Status               string
	NumberOfAssociations int
	LastModified         time.Time
	UpdateToken          string
	Tags                 map[string]string
}

// CreateTLSInspectionConfig is the input to CreateTLSInspectionConfiguration.
type CreateTLSInspectionConfig struct {
	Name          string
	Description   string
	Configuration json.RawMessage
	Tags          map[string]string
}

// UpdateTLSInspectionConfig is the input to UpdateTLSInspectionConfiguration.
// UpdateToken is required.
type UpdateTLSInspectionConfig struct {
	UpdateToken   string
	Description   string
	Configuration json.RawMessage
}

// TLSInspection covers the TLS inspection configuration family.
type TLSInspection interface {
	CreateTLSInspectionConfiguration(ctx context.Context, cfg CreateTLSInspectionConfig) (*TLSInspectionConfig, error)
	DescribeTLSInspectionConfiguration(ctx context.Context, name, arn string) (*TLSInspectionConfig, error)
	UpdateTLSInspectionConfiguration(ctx context.Context, name, arn string, cfg UpdateTLSInspectionConfig) (*TLSInspectionConfig, error)
	DeleteTLSInspectionConfiguration(ctx context.Context, name, arn string) (*TLSInspectionConfig, error)
	ListTLSInspectionConfigurations(ctx context.Context, opts ListOptions) ([]TLSInspectionConfig, string, error)
}

// VPCEndpointAssociation is an extra firewall endpoint in another VPC/subnet.
type VPCEndpointAssociation struct {
	ARN              string
	ID               string
	FirewallARN      string
	VPCID            string
	SubnetID         string
	IPAddressType    string
	AvailabilityZone string
	Description      string
	Status           string
	Tags             map[string]string
}

// CreateVPCEndpointAssociationConfig is the input to CreateVpcEndpointAssociation.
type CreateVPCEndpointAssociationConfig struct {
	FirewallARN   string
	VPCID         string
	SubnetID      string
	IPAddressType string
	Description   string
	Tags          map[string]string
}

// VPCEndpointAssociations covers the VPC endpoint association family.
type VPCEndpointAssociations interface {
	CreateVpcEndpointAssociation(ctx context.Context, cfg CreateVPCEndpointAssociationConfig) (*VPCEndpointAssociation, error)
	DescribeVpcEndpointAssociation(ctx context.Context, arn string) (*VPCEndpointAssociation, error)
	DeleteVpcEndpointAssociation(ctx context.Context, arn string) (*VPCEndpointAssociation, error)
	ListVpcEndpointAssociations(ctx context.Context, firewallARN string, opts ListOptions) ([]VPCEndpointAssociation, string, error)
}

// FirewallMetadata is the DescribeFirewallMetadata result.
type FirewallMetadata struct {
	FirewallARN       string
	PolicyARN         string
	Description       string
	Status            string
	AvailabilityZones []string
}

// RuleSummary is one entry of a rule group summary.
type RuleSummary struct {
	Msg      string
	SID      string
	Metadata string
}

// Metadata covers the read-only metadata/summary operations.
type Metadata interface {
	DescribeFirewallMetadata(ctx context.Context, firewallARN string) (*FirewallMetadata, error)
	DescribeRuleGroupMetadata(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	DescribeRuleGroupSummary(ctx context.Context, name, arn, ruleType string) (*RuleGroup, []RuleSummary, error)
}

// FlowOperation is a flow capture or flush request. The emulator has no data
// plane, so an operation completes without results.
type FlowOperation struct {
	ID                        string
	FirewallARN               string
	Type                      string // FLOW_CAPTURE | FLOW_FLUSH
	Status                    string
	StatusMessage             string
	AvailabilityZone          string
	VPCEndpointID             string
	VPCEndpointAssociationARN string
	MinimumFlowAgeSeconds     *int32
	Filters                   json.RawMessage
	RequestedAt               time.Time
}

// FlowOperationRequest is the input to StartFlowCapture / StartFlowFlush.
type FlowOperationRequest struct {
	FirewallARN               string
	AvailabilityZone          string
	VPCEndpointID             string
	VPCEndpointAssociationARN string
	MinimumFlowAgeSeconds     *int32
	Filters                   json.RawMessage
}

// FlowOperationScope identifies the firewall endpoint a flow operation ran on.
type FlowOperationScope struct {
	FirewallARN               string
	AvailabilityZone          string
	VPCEndpointID             string
	VPCEndpointAssociationARN string
}

// FlowOperations covers the flow capture / flush family.
type FlowOperations interface {
	StartFlowCapture(ctx context.Context, req FlowOperationRequest) (*FlowOperation, error)
	StartFlowFlush(ctx context.Context, req FlowOperationRequest) (*FlowOperation, error)
	DescribeFlowOperation(ctx context.Context, scope FlowOperationScope, id string) (*FlowOperation, error)
	ListFlowOperations(ctx context.Context, scope FlowOperationScope, opType string, opts ListOptions) ([]FlowOperation, string, error)
	// ListFlowOperationResults returns the operation and its (always empty, there is no
	// data plane) flows.
	ListFlowOperationResults(ctx context.Context, scope FlowOperationScope, id string, opts ListOptions) (*FlowOperation, string, error)
}

// AnalysisReport is a traffic analysis report request.
type AnalysisReport struct {
	ID           string
	FirewallARN  string
	AnalysisType string
	Status       string
	ReportTime   time.Time
}

// AnalysisReports covers the analysis report family.
type AnalysisReports interface {
	StartAnalysisReport(ctx context.Context, firewallName, firewallARN, analysisType string) (*AnalysisReport, error)
	ListAnalysisReports(ctx context.Context, firewallName, firewallARN string, opts ListOptions) ([]AnalysisReport, string, error)
	// GetAnalysisReport returns one report (its results are always empty).
	GetAnalysisReport(ctx context.Context, firewallName, firewallARN, reportID string) (*AnalysisReport, error)
}
