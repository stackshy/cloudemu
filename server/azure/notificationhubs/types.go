package notificationhubs

import (
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// ARM resource type strings stamped on responses.
const (
	namespaceResourceType = "Microsoft.NotificationHubs/namespaces"
	hubResourceType       = "Microsoft.NotificationHubs/namespaces/notificationHubs"
	defaultLocation       = "global"
	hubKeySep             = "/"
)

// --- namespace JSON ---

type namespaceProperties struct {
	Name              string `json:"name,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
	Status            string `json:"status,omitempty"`
	Enabled           *bool  `json:"enabled,omitempty"`
}

type namespaceJSON struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Location   string               `json:"location"`
	Tags       map[string]string    `json:"tags,omitempty"`
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

// --- request body (shared shape for namespace + hub PUT) ---

type putBody struct {
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *putProperties    `json:"properties,omitempty"`
}

type putProperties struct {
	Name            string `json:"name,omitempty"`
	RegistrationTTL string `json:"registrationTtl,omitempty"`
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

// toNamespaceJSON converts a driver topic into its ARM namespace element for the
// given path scope.
func toNamespaceJSON(rp *azurearm.ResourcePath, info *notifdriver.TopicInfo) namespaceJSON {
	return namespaceJSON{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNamespaces, info.Name),
		Name:     info.Name,
		Type:     namespaceResourceType,
		Location: defaultLocation,
		Tags:     info.Tags,
		Properties: &namespaceProperties{
			Name:              info.Name,
			ProvisioningState: "Succeeded",
			Status:            "Created/Active",
			Enabled:           enabled(),
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
		Location: defaultLocation,
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
