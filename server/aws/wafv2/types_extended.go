package wafv2

import (
	"encoding/json"

	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// --- CheckCapacity ---

type checkCapacityRequest struct {
	Scope string          `json:"Scope"`
	Rules json.RawMessage `json:"Rules"`
}

type checkCapacityResponse struct {
	Capacity int64 `json:"Capacity"`
}

// --- LoggingConfiguration (stored/echoed verbatim as raw JSON) ---

type putLoggingConfigurationRequest struct {
	LoggingConfiguration json.RawMessage `json:"LoggingConfiguration"`
}

type putLoggingConfigurationResponse struct {
	LoggingConfiguration json.RawMessage `json:"LoggingConfiguration,omitempty"`
}

type getLoggingConfigurationRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type getLoggingConfigurationResponse struct {
	LoggingConfiguration json.RawMessage `json:"LoggingConfiguration,omitempty"`
}

type deleteLoggingConfigurationRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type listLoggingConfigurationsResponse struct {
	LoggingConfigurations []json.RawMessage `json:"LoggingConfigurations"`
	NextMarker            string            `json:"NextMarker,omitempty"`
}

// --- PermissionPolicy ---

type putPermissionPolicyRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Policy      string `json:"Policy"`
}

type getPermissionPolicyRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type getPermissionPolicyResponse struct {
	Policy string `json:"Policy"`
}

type deletePermissionPolicyRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

// --- API keys ---

type createAPIKeyRequest struct {
	Scope        string   `json:"Scope"`
	TokenDomains []string `json:"TokenDomains"`
}

type createAPIKeyResponse struct {
	APIKey string `json:"APIKey"`
}

type deleteAPIKeyRequest struct {
	Scope  string `json:"Scope"`
	APIKey string `json:"APIKey"`
}

type listAPIKeysRequest struct {
	Scope string `json:"Scope"`
}

type apiKeySummaryJSON struct {
	APIKey            string   `json:"APIKey"`
	CreationTimestamp float64  `json:"CreationTimestamp"`
	TokenDomains      []string `json:"TokenDomains"`
	Version           int32    `json:"Version"`
}

func apiKeyToWire(s *wafdriver.APIKeySummary) apiKeySummaryJSON {
	domains := s.TokenDomains
	if domains == nil {
		domains = []string{}
	}

	return apiKeySummaryJSON{
		APIKey: s.APIKey, CreationTimestamp: float64(s.Created),
		TokenDomains: domains, Version: s.Version,
	}
}

type listAPIKeysResponse struct {
	APIKeySummaries           []apiKeySummaryJSON `json:"APIKeySummaries"`
	ApplicationIntegrationURL string              `json:"ApplicationIntegrationURL,omitempty"`
	NextMarker                string              `json:"NextMarker,omitempty"`
}

type getDecryptedAPIKeyRequest struct {
	Scope  string `json:"Scope"`
	APIKey string `json:"APIKey"`
}

type getDecryptedAPIKeyResponse struct {
	CreationTimestamp float64  `json:"CreationTimestamp"`
	TokenDomains      []string `json:"TokenDomains"`
}

// --- managed products / rule groups / mobile SDK / statistics (synthesized) ---
//
// These read-only catalog and analytics operations depend on AWS-managed vendor
// catalogs and live traffic that the emulator does not model. They return
// plausible empty (or, for GenerateMobileSdkReleaseUrl, synthesized) results so
// SDK/CLI calls succeed and round-trip, without pretending to serve a real
// vendor catalog or traffic sample.

type scopeRequest struct {
	Scope string `json:"Scope"`
}

type describeManagedProductsByVendorRequest struct {
	Scope      string `json:"Scope"`
	VendorName string `json:"VendorName"`
}

type describeManagedProductsResponse struct {
	ManagedProducts []json.RawMessage `json:"ManagedProducts"`
}

type describeManagedRuleGroupRequest struct {
	Name        string `json:"Name"`
	Scope       string `json:"Scope"`
	VendorName  string `json:"VendorName"`
	VersionName string `json:"VersionName"`
}

type describeManagedRuleGroupResponse struct {
	AvailableLabels []json.RawMessage `json:"AvailableLabels"`
	Capacity        int64             `json:"Capacity"`
	ConsumedLabels  []json.RawMessage `json:"ConsumedLabels"`
	LabelNamespace  string            `json:"LabelNamespace,omitempty"`
	Rules           []json.RawMessage `json:"Rules"`
	SnsTopicArn     string            `json:"SnsTopicArn,omitempty"`
	VersionName     string            `json:"VersionName,omitempty"`
}

type generateMobileSdkReleaseURLRequest struct {
	Platform       string `json:"Platform"`
	ReleaseVersion string `json:"ReleaseVersion"`
}

type generateMobileSdkReleaseURLResponse struct {
	URL string `json:"Url"`
}

type getMobileSdkReleaseRequest struct {
	Platform       string `json:"Platform"`
	ReleaseVersion string `json:"ReleaseVersion"`
}

type getMobileSdkReleaseResponse struct {
	MobileSdkRelease json.RawMessage `json:"MobileSdkRelease,omitempty"`
}

type listMobileSdkReleasesRequest struct {
	Platform string `json:"Platform"`
}

type listMobileSdkReleasesResponse struct {
	ReleaseSummaries []json.RawMessage `json:"ReleaseSummaries"`
	NextMarker       string            `json:"NextMarker,omitempty"`
}

type listAvailableManagedRuleGroupsResponse struct {
	ManagedRuleGroups []json.RawMessage `json:"ManagedRuleGroups"`
	NextMarker        string            `json:"NextMarker,omitempty"`
}

type listAvailableManagedRuleGroupVersionsRequest struct {
	Name       string `json:"Name"`
	Scope      string `json:"Scope"`
	VendorName string `json:"VendorName"`
}

type listAvailableManagedRuleGroupVersionsResponse struct {
	CurrentDefaultVersion string            `json:"CurrentDefaultVersion,omitempty"`
	Versions              []json.RawMessage `json:"Versions"`
	NextMarker            string            `json:"NextMarker,omitempty"`
}

type listManagedRuleSetsResponse struct {
	ManagedRuleSets []json.RawMessage `json:"ManagedRuleSets"`
	NextMarker      string            `json:"NextMarker,omitempty"`
}

type getManagedRuleSetRequest struct {
	Name  string `json:"Name"`
	Scope string `json:"Scope"`
	ID    string `json:"Id"`
}

type putManagedRuleSetVersionsRequest struct {
	Name      string `json:"Name"`
	Scope     string `json:"Scope"`
	ID        string `json:"Id"`
	LockToken string `json:"LockToken"`
}

type updateManagedRuleSetVersionExpiryDateRequest struct {
	Name            string  `json:"Name"`
	Scope           string  `json:"Scope"`
	ID              string  `json:"Id"`
	LockToken       string  `json:"LockToken"`
	VersionToExpire string  `json:"VersionToExpire"`
	ExpiryTimestamp float64 `json:"ExpiryTimestamp"`
}

type getRateBasedStatementManagedKeysResponse struct {
	ManagedKeysIPV4 rateBasedManagedKeysJSON `json:"ManagedKeysIPV4"`
	ManagedKeysIPV6 rateBasedManagedKeysJSON `json:"ManagedKeysIPV6"`
}

type rateBasedManagedKeysJSON struct {
	Addresses        []string `json:"Addresses"`
	IPAddressVersion string   `json:"IPAddressVersion,omitempty"`
}

// --- monetization / revenue / traffic statistics (synthesized empty) ---

type getSampledRequestsRequest struct {
	TimeWindow json.RawMessage `json:"TimeWindow"`
}

type getSampledRequestsResponse struct {
	PopulationSize  int64             `json:"PopulationSize"`
	SampledRequests []json.RawMessage `json:"SampledRequests"`
	TimeWindow      json.RawMessage   `json:"TimeWindow,omitempty"`
}

type getTopPathStatisticsByTrafficResponse struct {
	PathStatistics    []json.RawMessage `json:"PathStatistics"`
	TotalRequestCount int64             `json:"TotalRequestCount"`
	TopCategories     []json.RawMessage `json:"TopCategories"`
	NextMarker        string            `json:"NextMarker,omitempty"`
}

type getRevenueStatisticsResponse struct {
	RevenuePathStatistics []json.RawMessage `json:"RevenuePathStatistics"`
	SourceStatistics      []json.RawMessage `json:"SourceStatistics"`
	NextMarker            string            `json:"NextMarker,omitempty"`
}

type getRevenueStatisticsSummaryResponse struct {
	RevenueBreakdown json.RawMessage `json:"RevenueBreakdown,omitempty"`
}

type getRevenueStatisticsTimeSeriesResponse struct {
	DataPoints []json.RawMessage `json:"DataPoints"`
	NextMarker string            `json:"NextMarker,omitempty"`
}

type listSettlementRecordsResponse struct {
	Settlements []json.RawMessage `json:"Settlements"`
	NextMarker  string            `json:"NextMarker,omitempty"`
}

// --- DeleteFirewallManagerRuleGroups ---

type deleteFirewallManagerRuleGroupsRequest struct {
	WebACLArn       string `json:"WebACLArn"`
	WebACLLockToken string `json:"WebACLLockToken"`
}

type deleteFirewallManagerRuleGroupsResponse struct {
	NextWebACLLockToken string `json:"NextWebACLLockToken"`
}
