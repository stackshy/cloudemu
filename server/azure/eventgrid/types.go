package eventgrid

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	topicResourceType    = "Microsoft.EventGrid/topics"
	defaultTopicLocation = "global"
	// provisioningSucceeded is the terminal ProvisioningState Event Grid
	// reports once a topic is ready. Stamping it on the CreateOrUpdate response
	// lets the SDK's body-based LRO poller complete on the first response.
	provisioningSucceeded = "Succeeded"
	// defaultInputSchema and defaultPublicNetworkAccess are the values Event
	// Grid stamps on a topic created without overrides.
	defaultInputSchema          = "EventGridSchema"
	defaultPublicNetworkAccess  = "Enabled"
	subEventSubscriptions       = "eventSubscriptions"
	actionListKeys              = "listKeys"
	subscriptionResourceType    = "Microsoft.EventGrid/topics/eventSubscriptions"
	subscriptionProvisionedGood = "Succeeded"
)

// topicProperties carries the read-only fields the SDK expects on a topic. The
// endpoint is the data-plane publish URL derived from the topic name and region.
type topicProperties struct {
	ProvisioningState   string `json:"provisioningState,omitempty"`
	Endpoint            string `json:"endpoint,omitempty"`
	InputSchema         string `json:"inputSchema,omitempty"`
	PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
	MetricResourceID    string `json:"metricResourceId,omitempty"`
}

// topicJSON is the ARM Topic resource shape. Only the fields the SDK reads back
// are populated.
type topicJSON struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]*string `json:"tags,omitempty"`
	Properties *topicProperties   `json:"properties,omitempty"`
}

type topicListResult struct {
	Value []topicJSON `json:"value"`
}

// topicSharedAccessKeys is the Topics.ListSharedAccessKeys response.
type topicSharedAccessKeys struct {
	Key1 string `json:"key1"`
	Key2 string `json:"key2"`
}

// topicLocation returns the region a topic was created in, defaulting to
// "global" when the caller did not supply one.
func topicLocation(info *ebdriver.EventBusInfo) string {
	if info.Region != "" {
		return info.Region
	}

	return defaultTopicLocation
}

// topicEndpoint builds the data-plane publish endpoint Event Grid advertises
// for a topic: https://{name}.{region}-1.eventgrid.azure.net/api/events.
func topicEndpoint(name, location string) string {
	return "https://" + name + "." + location + "-1.eventgrid.azure.net/api/events"
}

// toTopicJSON converts a driver event bus into its ARM Topic element for the
// given path scope.
func toTopicJSON(rp *azurearm.ResourcePath, info *ebdriver.EventBusInfo) topicJSON {
	// Build the id (and the derived metricResourceId) from the topic's own
	// group, not the request path's — which is empty on a subscription-scoped
	// list — so the id carries its true resourceGroups/{rg} segment.
	rg := info.Scope.ResourceGroup
	if rg == "" {
		rg = rp.ResourceGroup
	}

	id := azurearm.BuildResourceID(rp.Subscription, rg, providerName, typeTopics, info.Name)
	loc := topicLocation(info)

	inputSchema := info.InputSchema
	if inputSchema == "" {
		inputSchema = defaultInputSchema
	}

	publicNetworkAccess := info.PublicNetworkAccess
	if publicNetworkAccess == "" {
		publicNetworkAccess = defaultPublicNetworkAccess
	}

	return topicJSON{
		ID:       id,
		Name:     info.Name,
		Type:     topicResourceType,
		Location: loc,
		Tags:     tagsToPtr(info.Tags),
		Properties: &topicProperties{
			ProvisioningState:   provisioningSucceeded,
			Endpoint:            topicEndpoint(info.Name, loc),
			InputSchema:         inputSchema,
			PublicNetworkAccess: publicNetworkAccess,
			MetricResourceID:    id,
		},
	}
}

// tagsToPtr converts the driver's flat tag map to ARM's map[string]*string.
func tagsToPtr(tags map[string]string) map[string]*string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]*string, len(tags))
	for k, v := range tags {
		val := v
		out[k] = &val
	}

	return out
}

// tagsFromPtr converts ARM's map[string]*string tags to the driver's flat map.
func tagsFromPtr(tags map[string]*string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			out[k] = *v
		}
	}

	return out
}
