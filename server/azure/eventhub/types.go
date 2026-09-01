package eventhub

import "time"

// namespaceResource is the ARM JSON shape for Microsoft.EventHub/namespaces.
type namespaceResource struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location"`
	Tags       map[string]string   `json:"tags,omitempty"`
	SKU        *ehSKU              `json:"sku,omitempty"`
	SystemData *systemData         `json:"systemData,omitempty"`
	Properties namespaceProperties `json:"properties"`
}

type namespaceProperties struct {
	ProvisioningState      string     `json:"provisioningState,omitempty"`
	Status                 string     `json:"status,omitempty"`
	ServiceBusEndpoint     string     `json:"serviceBusEndpoint,omitempty"`
	MetricID               string     `json:"metricId,omitempty"`
	IsAutoInflateEnabled   *bool      `json:"isAutoInflateEnabled,omitempty"`
	MaximumThroughputUnits *int32     `json:"maximumThroughputUnits,omitempty"`
	KafkaEnabled           *bool      `json:"kafkaEnabled,omitempty"`
	ZoneRedundant          *bool      `json:"zoneRedundant,omitempty"`
	DisableLocalAuth       *bool      `json:"disableLocalAuth,omitempty"`
	CreatedAt              *time.Time `json:"createdAt,omitempty"`
	UpdatedAt              *time.Time `json:"updatedAt,omitempty"`
}

type ehSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity *int32 `json:"capacity,omitempty"`
}

type systemData struct {
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
	CreatedByType  string     `json:"createdByType,omitempty"`
	LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`
}

// eventHubResource is the ARM JSON shape for .../namespaces/eventhubs.
type eventHubResource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Properties eventHubProperties `json:"properties"`
}

type eventHubProperties struct {
	PartitionCount         *int64     `json:"partitionCount,omitempty"`
	MessageRetentionInDays *int64     `json:"messageRetentionInDays,omitempty"`
	Status                 string     `json:"status,omitempty"`
	PartitionIDs           []string   `json:"partitionIds,omitempty"`
	CreatedAt              *time.Time `json:"createdAt,omitempty"`
	UpdatedAt              *time.Time `json:"updatedAt,omitempty"`
}

// consumerGroupResource is the ARM JSON shape for
// .../eventhubs/consumergroups.
type consumerGroupResource struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Type       string                  `json:"type"`
	Properties consumerGroupProperties `json:"properties"`
}

type consumerGroupProperties struct {
	UserMetadata string     `json:"userMetadata,omitempty"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// authRuleResource is the ARM JSON shape for .../authorizationRules (at both
// namespace and event-hub scope).
type authRuleResource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Properties authRuleProperties `json:"properties"`
}

type authRuleProperties struct {
	Rights []string `json:"rights"`
}

// accessKeys is the ARM JSON shape returned by listKeys / regenerateKeys.
type accessKeys struct {
	PrimaryConnectionString   string `json:"primaryConnectionString"`
	SecondaryConnectionString string `json:"secondaryConnectionString"`
	PrimaryKey                string `json:"primaryKey"`
	SecondaryKey              string `json:"secondaryKey"`
	KeyName                   string `json:"keyName"`
}

// checkNameResult is the ARM JSON shape for CheckNameAvailability.
type checkNameResult struct {
	NameAvailable bool   `json:"nameAvailable"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
}

// listResponse is the {value: [...]} envelope ARM uses for collection responses.
type listResponse struct {
	Value    []any  `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// Request bodies decoded from PUT/POST payloads.

type createNamespaceRequest struct {
	Location   string              `json:"location"`
	Tags       map[string]string   `json:"tags,omitempty"`
	SKU        *ehSKU              `json:"sku,omitempty"`
	Properties namespaceProperties `json:"properties"`
}

type createEventHubRequest struct {
	Properties eventHubProperties `json:"properties"`
}

type createConsumerGroupRequest struct {
	Properties consumerGroupProperties `json:"properties"`
}

type createAuthRuleRequest struct {
	Properties authRuleProperties `json:"properties"`
}

type regenerateKeysRequest struct {
	KeyType string `json:"keyType"`
	Key     string `json:"key,omitempty"`
}

type checkNameRequest struct {
	Name string `json:"name"`
}
