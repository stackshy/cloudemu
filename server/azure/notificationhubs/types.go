package notificationhubs

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// ARM resource type strings stamped on responses.
const (
	namespaceResourceType = "Microsoft.NotificationHubs/namespaces"
	hubResourceType       = "Microsoft.NotificationHubs/namespaces/notificationHubs"
	nsAuthRuleType        = "Microsoft.NotificationHubs/namespaces/AuthorizationRules"
	hubAuthRuleType       = "Microsoft.NotificationHubs/namespaces/notificationHubs/AuthorizationRules"
	defaultLocation       = "global"
	hubKeySep             = "/"

	subAuthorizationRules = "AuthorizationRules"
	subNotificationHubs   = "notificationHubs"
	actionListKeys        = "listKeys"
	typeCheckNSAvail      = "checkNamespaceAvailability"

	namespaceTypeValue = "NotificationHub"
	namespaceStatusVal = "Created"
	provisioningState  = "Succeeded"
)

// --- namespace JSON ---

type sku struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type namespaceProperties struct {
	Name               string `json:"name,omitempty"`
	ProvisioningState  string `json:"provisioningState,omitempty"`
	Status             string `json:"status,omitempty"`
	Enabled            *bool  `json:"enabled,omitempty"`
	ServiceBusEndpoint string `json:"serviceBusEndpoint,omitempty"`
	NamespaceType      string `json:"namespaceType,omitempty"`
	MetricID           string `json:"metricId,omitempty"`
	Region             string `json:"region,omitempty"`
	SubscriptionID     string `json:"subscriptionId,omitempty"`
}

type namespaceJSON struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Location   string               `json:"location"`
	Tags       map[string]string    `json:"tags,omitempty"`
	SKU        *sku                 `json:"sku,omitempty"`
	Properties *namespaceProperties `json:"properties,omitempty"`
}

type namespaceListResult struct {
	Value []namespaceJSON `json:"value"`
}

// --- notification hub JSON ---

type hubProperties struct {
	Name            string `json:"name,omitempty"`
	RegistrationTTL string `json:"registrationTtl,omitempty"`
}

type hubJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *hubProperties    `json:"properties,omitempty"`
}

type hubListResult struct {
	Value []hubJSON `json:"value"`
}

// --- authorization rules + keys ---

type authRuleProperties struct {
	Rights       []string `json:"rights,omitempty"`
	PrimaryKey   string   `json:"primaryKey,omitempty"`
	SecondaryKey string   `json:"secondaryKey,omitempty"`
	KeyName      string   `json:"keyName,omitempty"`
	ClaimType    string   `json:"claimType,omitempty"`
	ClaimValue   string   `json:"claimValue,omitempty"`
}

type authRuleJSON struct {
	ID         string              `json:"id,omitempty"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location,omitempty"`
	Properties *authRuleProperties `json:"properties,omitempty"`
}

type authRuleListResult struct {
	Value []authRuleJSON `json:"value"`
}

// resourceListKeys is the ListKeys response for a SAS rule.
type resourceListKeys struct {
	KeyName                   string `json:"keyName,omitempty"`
	PrimaryKey                string `json:"primaryKey,omitempty"`
	SecondaryKey              string `json:"secondaryKey,omitempty"`
	PrimaryConnectionString   string `json:"primaryConnectionString,omitempty"`
	SecondaryConnectionString string `json:"secondaryConnectionString,omitempty"`
}

// checkAvailabilityResult is the CheckAvailability response.
type checkAvailabilityResult struct {
	Name         string `json:"name,omitempty"`
	IsAvailiable bool   `json:"isAvailiable"`
	Location     string `json:"location,omitempty"`
}

// --- request bodies ---

type putBody struct {
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *sku              `json:"sku,omitempty"`
	Properties *putProperties    `json:"properties,omitempty"`
}

type putProperties struct {
	Name            string `json:"name,omitempty"`
	RegistrationTTL string `json:"registrationTtl,omitempty"`
}

type authRulePutBody struct {
	Properties *authRulePutProps `json:"properties,omitempty"`
}

type authRulePutProps struct {
	Rights []string `json:"rights,omitempty"`
}

type checkAvailabilityBody struct {
	Name string `json:"name,omitempty"`
}

// enabled is a helper for populating the *bool namespace enabled flag.
func enabled() *bool {
	v := true
	return &v
}

// hubKey builds the driver topic key for a hub nested under a namespace.
func hubKey(namespace, hub string) string {
	return namespace + hubKeySep + hub
}

// nsLocation returns the namespace location, defaulting to "global".
func nsLocation(info *notifdriver.TopicInfo) string {
	if info.Region != "" {
		return info.Region
	}

	return defaultLocation
}

// serviceBusEndpoint builds the ARM serviceBusEndpoint for a namespace.
func serviceBusEndpoint(namespace string) string {
	return "https://" + namespace + ".servicebus.windows.net:443/"
}

// sasConnectionString builds an Azure SAS connection string for a rule.
func sasConnectionString(namespace, ruleName, key string) string {
	return "Endpoint=sb://" + namespace + ".servicebus.windows.net/;SharedAccessKeyName=" +
		ruleName + ";SharedAccessKey=" + key
}

// toNamespaceJSON converts a driver topic into its ARM namespace element.
func toNamespaceJSON(rp *azurearm.ResourcePath, info *notifdriver.TopicInfo, skuName string) namespaceJSON {
	rg := rp.ResourceGroup
	if rg == "" {
		rg = info.Scope.ResourceGroup
	}

	id := azurearm.BuildResourceID(rp.Subscription, rg, providerName, typeNamespaces, info.Name)

	var skuJSON *sku
	if skuName != "" {
		skuJSON = &sku{Name: skuName, Tier: skuName}
	}

	return namespaceJSON{
		ID:       id,
		Name:     info.Name,
		Type:     namespaceResourceType,
		Location: nsLocation(info),
		Tags:     info.Tags,
		SKU:      skuJSON,
		Properties: &namespaceProperties{
			Name:               info.Name,
			ProvisioningState:  provisioningState,
			Status:             namespaceStatusVal,
			Enabled:            enabled(),
			ServiceBusEndpoint: serviceBusEndpoint(info.Name),
			NamespaceType:      namespaceTypeValue,
			MetricID:           id,
			Region:             nsLocation(info),
			SubscriptionID:     rp.Subscription,
		},
	}
}

// toHubJSON converts a driver topic into its ARM notification-hub element. The
// SDK-facing hub name (hubName) is the bare hub name, not the composite key.
func toHubJSON(rp *azurearm.ResourcePath, namespace, hubName string, info *notifdriver.TopicInfo) hubJSON {
	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNamespaces, namespace) +
		"/" + subHubs + "/" + hubName

	return hubJSON{
		ID:       id,
		Name:     hubName,
		Type:     hubResourceType,
		Location: nsLocation(info),
		Tags:     info.Tags,
		Properties: &hubProperties{
			Name:            hubName,
			RegistrationTTL: propRegistrationTTL(info),
		},
	}
}

// propRegistrationTTL surfaces the stored registration TTL, if any. The driver
// carries it in the topic's display name (the closest scalar field available).
func propRegistrationTTL(info *notifdriver.TopicInfo) string {
	return info.DisplayName
}
