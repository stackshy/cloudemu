package acr

import (
	"time"

	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// ARM resource type identifiers for the Microsoft.ContainerRegistry provider.
const (
	armProviderName        = "Microsoft.ContainerRegistry"
	resourceTypeRegistries = "registries"
	registryTypeFull       = "Microsoft.ContainerRegistry/registries"
	webhookTypeFull        = "Microsoft.ContainerRegistry/registries/webhooks"
	replicationTypeFull    = "Microsoft.ContainerRegistry/registries/replications"
)

// armRegistry mirrors armcontainerregistry.Registry.
type armRegistry struct {
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Location   string                 `json:"location,omitempty"`
	Tags       map[string]*string     `json:"tags,omitempty"`
	SKU        *armRegistrySKU        `json:"sku,omitempty"`
	Identity   *armRegistryIdentity   `json:"identity,omitempty"`
	Properties *armRegistryProperties `json:"properties,omitempty"`
}

type armRegistrySKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type armRegistryIdentity struct {
	Type                   string                              `json:"type,omitempty"`
	PrincipalID            string                              `json:"principalId,omitempty"`
	TenantID               string                              `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]*armUserAssignedIdentity `json:"userAssignedIdentities,omitempty"`
}

// armUserAssignedIdentity mirrors armcontainerregistry.UserIdentityProperties:
// the principal/client pair Azure returns for one attached user-assigned
// identity. On a request the values are empty ({}); the response synthesizes
// them.
type armUserAssignedIdentity struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

type armRegistryProperties struct {
	LoginServer       string `json:"loginServer,omitempty"`
	CreationDate      string `json:"creationDate,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
	AdminUserEnabled  bool   `json:"adminUserEnabled"`
}

// armRegistryUpdateParameters mirrors armcontainerregistry.RegistryUpdateParameters
// for ARM PATCH decoding. Every field is a pointer so an absent field is
// distinguishable from a zero value and left untouched during the partial merge;
// modeling AdminUserEnabled as a bare bool would silently reset it to false on
// any PATCH that only touches another property.
type armRegistryUpdateParameters struct {
	Tags       map[string]*string      `json:"tags,omitempty"`
	SKU        *armRegistrySKU         `json:"sku,omitempty"`
	Identity   *armRegistryIdentity    `json:"identity,omitempty"`
	Properties *armRegistryUpdateProps `json:"properties,omitempty"`
}

type armRegistryUpdateProps struct {
	AdminUserEnabled *bool `json:"adminUserEnabled,omitempty"`
}

// armRegistryListCredentialsResult mirrors RegistryListCredentialsResult.
type armRegistryListCredentialsResult struct {
	Username  string                `json:"username,omitempty"`
	Passwords []armRegistryPassword `json:"passwords,omitempty"`
}

type armRegistryPassword struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// armRegenerateCredentialParameters mirrors RegenerateCredentialParameters.
type armRegenerateCredentialParameters struct {
	Name string `json:"name,omitempty"`
}

// armRegistryUsageListResult mirrors RegistryUsageListResult.
type armRegistryUsageListResult struct {
	Value []armRegistryUsage `json:"value"`
}

type armRegistryUsage struct {
	Name         string `json:"name,omitempty"`
	Limit        int64  `json:"limit"`
	CurrentValue int64  `json:"currentValue"`
	Unit         string `json:"unit,omitempty"`
}

// armWebhook mirrors armcontainerregistry.Webhook. Properties on create carry
// serviceUri and customHeaders (WebhookPropertiesCreateParameters); the plain
// GET response omits them (WebhookProperties) — they are exposed only via
// getCallbackConfig. toARMWebhook builds the read shape (omitting them) and
// toARMWebhookWithCallback the create/update shape (including them).
type armWebhook struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Type       string             `json:"type,omitempty"`
	Location   string             `json:"location,omitempty"`
	Tags       map[string]*string `json:"tags,omitempty"`
	Properties *armWebhookProps   `json:"properties,omitempty"`
}

type armWebhookProps struct {
	ServiceURI        string             `json:"serviceUri,omitempty"`
	Actions           []string           `json:"actions,omitempty"`
	Scope             string             `json:"scope,omitempty"`
	Status            string             `json:"status,omitempty"`
	CustomHeaders     map[string]*string `json:"customHeaders,omitempty"`
	ProvisioningState string             `json:"provisioningState,omitempty"`
}

// armReplication mirrors armcontainerregistry.Replication.
type armReplication struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name,omitempty"`
	Type       string               `json:"type,omitempty"`
	Location   string               `json:"location,omitempty"`
	Tags       map[string]*string   `json:"tags,omitempty"`
	Properties *armReplicationProps `json:"properties,omitempty"`
}

type armReplicationProps struct {
	RegionEndpointEnabled bool                  `json:"regionEndpointEnabled"`
	ProvisioningState     string                `json:"provisioningState,omitempty"`
	Status                *armReplicationStatus `json:"status,omitempty"`
}

type armReplicationStatus struct {
	DisplayStatus string `json:"displayStatus,omitempty"`
}

// armRegistryList is the ARM list envelope for registries.
type armRegistryList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

func toARMRegistry(reg *crdriver.AzureRegistry, subscription string) armRegistry {
	out := armRegistry{
		ID:       registryResourceID(subscription, reg.ResourceGroup, reg.Name),
		Name:     reg.Name,
		Type:     registryTypeFull,
		Location: reg.Location,
		Tags:     toPtrTags(reg.Tags),
		SKU:      &armRegistrySKU{Name: reg.SKUName, Tier: reg.SKUTier},
		Properties: &armRegistryProperties{
			LoginServer:       reg.LoginServer,
			CreationDate:      reg.CreationDate.UTC().Format(time.RFC3339),
			ProvisioningState: reg.ProvisioningState,
			AdminUserEnabled:  reg.AdminUserEnabled,
		},
	}

	if reg.IdentityType != "" && reg.IdentityType != "None" {
		out.Identity = &armRegistryIdentity{
			Type:                   reg.IdentityType,
			PrincipalID:            reg.PrincipalID,
			TenantID:               reg.TenantID,
			UserAssignedIdentities: toARMUserAssigned(reg.UserAssignedIdentities),
		}
	}

	return out
}

// toARMUserAssigned echoes each stored user-assigned identity with its
// synthesized principal/client pair, keyed by the identity resource ID.
func toARMUserAssigned(in map[string]crdriver.AzureUserAssignedIdentity) map[string]*armUserAssignedIdentity {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]*armUserAssignedIdentity, len(in))
	for id, v := range in {
		out[id] = &armUserAssignedIdentity{PrincipalID: v.PrincipalID, ClientID: v.ClientID}
	}

	return out
}

// toARMWebhook is the read (GET/List) shape: it deliberately omits serviceUri
// and customHeaders, mirroring real ACR's Webhooks_Get (WebhookProperties). The
// two write-only fields are retrievable only via getCallbackConfig.
func toARMWebhook(wh *crdriver.AzureWebhook, subscription string) armWebhook {
	return armWebhook{
		ID:       registryResourceID(subscription, wh.ResourceGroup, wh.RegistryName) + "/webhooks/" + wh.Name,
		Name:     wh.Name,
		Type:     webhookTypeFull,
		Location: wh.Location,
		Tags:     toPtrTags(wh.Tags),
		Properties: &armWebhookProps{
			Actions:           wh.Actions,
			Scope:             wh.Scope,
			Status:            wh.Status,
			ProvisioningState: wh.ProvisioningState,
		},
	}
}

// toARMWebhookWithCallback is the create/update (PUT/PATCH) response shape: it
// carries serviceUri and customHeaders so the Azure property overlay sees them
// as modeled and does NOT capture-and-replay them onto later plain GETs (which
// use toARMWebhook and must never leak them). Real ACR exposes these two only
// through getCallbackConfig.
func toARMWebhookWithCallback(wh *crdriver.AzureWebhook, subscription string) armWebhook {
	out := toARMWebhook(wh, subscription)
	out.Properties.ServiceURI = wh.ServiceURI
	out.Properties.CustomHeaders = toPtrTags(wh.CustomHeaders)

	return out
}

// armCallbackConfig mirrors armcontainerregistry.CallbackConfig, the sole read
// path for a webhook's serviceUri and customHeaders (getCallbackConfig).
type armCallbackConfig struct {
	ServiceURI    string             `json:"serviceUri,omitempty"`
	CustomHeaders map[string]*string `json:"customHeaders,omitempty"`
}

func toARMReplication(rep *crdriver.AzureReplication, subscription string) armReplication {
	return armReplication{
		ID:       registryResourceID(subscription, rep.ResourceGroup, rep.RegistryName) + "/replications/" + rep.Name,
		Name:     rep.Name,
		Type:     replicationTypeFull,
		Location: rep.Location,
		Tags:     toPtrTags(rep.Tags),
		Properties: &armReplicationProps{
			RegionEndpointEnabled: rep.RegionEndpointEnabled,
			ProvisioningState:     rep.ProvisioningState,
			Status:                &armReplicationStatus{DisplayStatus: rep.Status},
		},
	}
}

func registryResourceID(subscription, rg, name string) string {
	return "/subscriptions/" + subscription +
		"/resourceGroups/" + rg +
		"/providers/" + armProviderName +
		"/registries/" + name
}

func toPtrTags(in map[string]string) map[string]*string {
	if in == nil {
		return nil
	}

	out := make(map[string]*string, len(in))

	for k, v := range in {
		val := v
		out[k] = &val
	}

	return out
}

func fromPtrTags(in map[string]*string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if v != nil {
			out[k] = *v
		}
	}

	return out
}
