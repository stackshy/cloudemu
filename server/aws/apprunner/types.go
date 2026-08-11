package apprunner

import (
	"encoding/json"
	"sort"
	"time"
)

// epoch renders a time as AWS JSON 1.0 epoch seconds (fractional). A zero time
// serializes as nil so optional timestamps are omitted.
func epoch(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.UnixNano()) / float64(time.Second)

	return &secs
}

// tag is the App Runner wire tag shape. Unlike Step Functions (lowercase
// key/value), App Runner capitalizes Key/Value.
type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToMap(tags []tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

// mapToTags renders a tag map as the App Runner wire tag list, ordered by key
// for deterministic output.
func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })

	return out
}

// --- Service request shapes ---

type createServiceRequest struct {
	ServiceName                 string          `json:"ServiceName"`
	SourceConfiguration         json.RawMessage `json:"SourceConfiguration"`
	InstanceConfiguration       json.RawMessage `json:"InstanceConfiguration"`
	NetworkConfiguration        json.RawMessage `json:"NetworkConfiguration"`
	HealthCheckConfiguration    json.RawMessage `json:"HealthCheckConfiguration"`
	EncryptionConfiguration     json.RawMessage `json:"EncryptionConfiguration"`
	ObservabilityConfiguration  json.RawMessage `json:"ObservabilityConfiguration"`
	AutoScalingConfigurationArn string          `json:"AutoScalingConfigurationArn"`
	Tags                        []tag           `json:"Tags"`
}

type serviceArnRequest struct {
	ServiceArn string `json:"ServiceArn"`
}

type updateServiceRequest struct {
	ServiceArn                  string          `json:"ServiceArn"`
	SourceConfiguration         json.RawMessage `json:"SourceConfiguration"`
	InstanceConfiguration       json.RawMessage `json:"InstanceConfiguration"`
	NetworkConfiguration        json.RawMessage `json:"NetworkConfiguration"`
	HealthCheckConfiguration    json.RawMessage `json:"HealthCheckConfiguration"`
	ObservabilityConfiguration  json.RawMessage `json:"ObservabilityConfiguration"`
	AutoScalingConfigurationArn string          `json:"AutoScalingConfigurationArn"`
}

type listServicesRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

// --- Service response shapes ---

// wireService is the full Service wire shape. Modeled-verbatim config blocks are
// emitted as raw JSON; identity/status/timestamps are modeled explicitly.
type wireService struct {
	ServiceArn                      string          `json:"ServiceArn"`
	ServiceID                       string          `json:"ServiceId"`
	ServiceName                     string          `json:"ServiceName"`
	ServiceURL                      string          `json:"ServiceUrl,omitempty"`
	Status                          string          `json:"Status"`
	CreatedAt                       *float64        `json:"CreatedAt"`
	UpdatedAt                       *float64        `json:"UpdatedAt"`
	DeletedAt                       *float64        `json:"DeletedAt,omitempty"`
	SourceConfiguration             json.RawMessage `json:"SourceConfiguration,omitempty"`
	InstanceConfiguration           json.RawMessage `json:"InstanceConfiguration,omitempty"`
	NetworkConfiguration            json.RawMessage `json:"NetworkConfiguration,omitempty"`
	HealthCheckConfiguration        json.RawMessage `json:"HealthCheckConfiguration,omitempty"`
	EncryptionConfiguration         json.RawMessage `json:"EncryptionConfiguration,omitempty"`
	ObservabilityConfiguration      json.RawMessage `json:"ObservabilityConfiguration,omitempty"`
	AutoScalingConfigurationSummary *ascSummary     `json:"AutoScalingConfigurationSummary,omitempty"`
}

type ascSummary struct {
	AutoScalingConfigurationArn      string `json:"AutoScalingConfigurationArn,omitempty"`
	AutoScalingConfigurationName     string `json:"AutoScalingConfigurationName,omitempty"`
	AutoScalingConfigurationRevision int32  `json:"AutoScalingConfigurationRevision,omitempty"`
}

type serviceOpResponse struct {
	Service     wireService `json:"Service"`
	OperationID string      `json:"OperationId,omitempty"`
}

type describeServiceResponse struct {
	Service wireService `json:"Service"`
}

type serviceSummary struct {
	ServiceArn  string   `json:"ServiceArn"`
	ServiceID   string   `json:"ServiceId"`
	ServiceName string   `json:"ServiceName"`
	ServiceURL  string   `json:"ServiceUrl,omitempty"`
	Status      string   `json:"Status"`
	CreatedAt   *float64 `json:"CreatedAt"`
	UpdatedAt   *float64 `json:"UpdatedAt"`
}

type listServicesResponse struct {
	ServiceSummaryList []serviceSummary `json:"ServiceSummaryList"`
	NextToken          string           `json:"NextToken,omitempty"`
}

type startDeploymentResponse struct {
	OperationID string `json:"OperationId"`
}

// --- AutoScalingConfiguration shapes ---

type createASCRequest struct {
	AutoScalingConfigurationName string `json:"AutoScalingConfigurationName"`
	MaxConcurrency               int32  `json:"MaxConcurrency"`
	MaxSize                      int32  `json:"MaxSize"`
	MinSize                      int32  `json:"MinSize"`
}

type ascArnRequest struct {
	AutoScalingConfigurationArn string `json:"AutoScalingConfigurationArn"`
	DeleteAllRevisions          bool   `json:"DeleteAllRevisions"`
}

type listASCRequest struct {
	AutoScalingConfigurationName string `json:"AutoScalingConfigurationName"`
	LatestOnly                   bool   `json:"LatestOnly"`
	NextToken                    string `json:"NextToken"`
	MaxResults                   int32  `json:"MaxResults"`
}

type listServicesForASCRequest struct {
	AutoScalingConfigurationArn string `json:"AutoScalingConfigurationArn"`
	NextToken                   string `json:"NextToken"`
	MaxResults                  int32  `json:"MaxResults"`
}

type wireASC struct {
	AutoScalingConfigurationArn      string   `json:"AutoScalingConfigurationArn"`
	AutoScalingConfigurationName     string   `json:"AutoScalingConfigurationName"`
	AutoScalingConfigurationRevision int32    `json:"AutoScalingConfigurationRevision"`
	Status                           string   `json:"Status"`
	MaxConcurrency                   int32    `json:"MaxConcurrency"`
	MaxSize                          int32    `json:"MaxSize"`
	MinSize                          int32    `json:"MinSize"`
	IsDefault                        bool     `json:"IsDefault"`
	Latest                           bool     `json:"Latest"`
	HasAssociatedService             bool     `json:"HasAssociatedService"`
	CreatedAt                        *float64 `json:"CreatedAt"`
	DeletedAt                        *float64 `json:"DeletedAt,omitempty"`
}

type ascResponse struct {
	AutoScalingConfiguration wireASC `json:"AutoScalingConfiguration"`
}

type ascSummaryItem struct {
	AutoScalingConfigurationArn      string   `json:"AutoScalingConfigurationArn"`
	AutoScalingConfigurationName     string   `json:"AutoScalingConfigurationName"`
	AutoScalingConfigurationRevision int32    `json:"AutoScalingConfigurationRevision"`
	Status                           string   `json:"Status"`
	IsDefault                        bool     `json:"IsDefault"`
	HasAssociatedService             bool     `json:"HasAssociatedService"`
	CreatedAt                        *float64 `json:"CreatedAt"`
}

type listASCResponse struct {
	AutoScalingConfigurationSummaryList []ascSummaryItem `json:"AutoScalingConfigurationSummaryList"`
	NextToken                           string           `json:"NextToken,omitempty"`
}

type listServicesForASCResponse struct {
	ServiceArnList []string `json:"ServiceArnList"`
	NextToken      string   `json:"NextToken,omitempty"`
}

// --- Connection shapes ---

type createConnectionRequest struct {
	ConnectionName string `json:"ConnectionName"`
	ProviderType   string `json:"ProviderType"`
	Tags           []tag  `json:"Tags"`
}

type connectionArnRequest struct {
	ConnectionArn string `json:"ConnectionArn"`
}

type listConnectionsRequest struct {
	ConnectionName string `json:"ConnectionName"`
	NextToken      string `json:"NextToken"`
	MaxResults     int32  `json:"MaxResults"`
}

type wireConnection struct {
	ConnectionArn  string   `json:"ConnectionArn"`
	ConnectionName string   `json:"ConnectionName"`
	ProviderType   string   `json:"ProviderType"`
	Status         string   `json:"Status"`
	CreatedAt      *float64 `json:"CreatedAt"`
}

type connectionResponse struct {
	Connection wireConnection `json:"Connection"`
}

type connectionSummaryItem struct {
	ConnectionArn  string   `json:"ConnectionArn"`
	ConnectionName string   `json:"ConnectionName"`
	ProviderType   string   `json:"ProviderType"`
	Status         string   `json:"Status"`
	CreatedAt      *float64 `json:"CreatedAt"`
}

type listConnectionsResponse struct {
	ConnectionSummaryList []connectionSummaryItem `json:"ConnectionSummaryList"`
	NextToken             string                  `json:"NextToken,omitempty"`
}
