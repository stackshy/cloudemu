package eventgrid

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	actionGetFullURL            = "getFullUrl"
	actionGetDeliveryAttributes = "getDeliveryAttributes"
	webHookEndpointType         = "WebHook"
)

// isEventSubAction reports whether seg is one of the POST actions an event
// subscription exposes.
func isEventSubAction(seg string) bool {
	return strings.EqualFold(seg, actionGetFullURL) || strings.EqualFold(seg, actionGetDeliveryAttributes)
}

// subscriptionDestination is the part of the stored subscription properties
// the actions read.
type subscriptionDestination struct {
	Destination struct {
		EndpointType string `json:"endpointType"`
		Properties   struct {
			EndpointURL               string            `json:"endpointUrl"`
			DeliveryAttributeMappings []json.RawMessage `json:"deliveryAttributeMappings"`
		} `json:"properties"`
	} `json:"destination"`
}

// serveEventSubAction answers getFullUrl and getDeliveryAttributes from a
// subscription's stored properties. found is false when the subscription (or
// its parent) does not exist. getFullUrl is only defined for a WebHook
// destination; any other destination is a 400, as in real Azure.
func serveEventSubAction(w http.ResponseWriter, r *http.Request, action string, props json.RawMessage, found bool) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	if !found {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "event subscription not found")
		return
	}

	var d subscriptionDestination
	if len(props) > 0 {
		_ = json.Unmarshal(props, &d)
	}

	dest := d.Destination

	if strings.EqualFold(action, actionGetDeliveryAttributes) {
		mappings := dest.Properties.DeliveryAttributeMappings
		if mappings == nil {
			mappings = []json.RawMessage{}
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": mappings})

		return
	}

	if !strings.EqualFold(dest.EndpointType, webHookEndpointType) {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest",
			"getFullUrl is only supported for WebHook destinations")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{"endpointUrl": dest.Properties.EndpointURL})
}

// serveTopicSubAction answers an action on a custom topic's subscription,
// whose properties the eventbus rule holds.
func (h *Handler) serveTopicSubAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, action string) {
	var props json.RawMessage

	rule, err := h.bus.GetRule(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err == nil {
		props = json.RawMessage(rule.Description)
	}

	serveEventSubAction(w, r, action, props, err == nil)
}

// eventSubActionOf returns the action a topic or system-topic subscription path
// addresses, or "" when it addresses the subscription itself.
func eventSubActionOf(rp *azurearm.ResourcePath) string {
	if rp.SubResourceName == "" || rp.Rest != "" || !isEventSubAction(rp.SubResourceAction) {
		return ""
	}

	return rp.SubResourceAction
}
