package eventgrid

import (
	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	topicResourceType    = "Microsoft.EventGrid/topics"
	defaultTopicLocation = "global"
	// provisioningSucceeded is the terminal ProvisioningState Event Grid
	// reports once a topic is ready. Stamping it on the CreateOrUpdate response
	// lets the SDK's body-based LRO poller complete on the first response.
	provisioningSucceeded = "Succeeded"
)

// topicProperties carries the read-only provisioning state and endpoint the SDK
// expects on a topic. The eventbus driver has no endpoint concept, so Endpoint
// is left empty.
type topicProperties struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
	Endpoint          string `json:"endpoint,omitempty"`
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

// toTopicJSON converts a driver event bus into its ARM Topic element for the
// given path scope. Event Grid topics are always "global" location.
func toTopicJSON(rp *azurearm.ResourcePath, info *ebdriver.EventBusInfo) topicJSON {
	return topicJSON{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeTopics, info.Name),
		Name:     info.Name,
		Type:     topicResourceType,
		Location: defaultTopicLocation,
		Tags:     tagsToPtr(info.Tags),
		Properties: &topicProperties{
			ProvisioningState: provisioningSucceeded,
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
