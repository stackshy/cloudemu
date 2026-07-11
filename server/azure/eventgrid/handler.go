// Package eventgrid implements the Azure Event Grid
// (Microsoft.EventGrid/topics) ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com, driving the shared eventbus driver.
//
// Event Grid topics map onto the eventbus driver's event buses: a topic is an
// event bus keyed by its user-assigned name. The driver's rule/target and
// event-publish model has no ARM management-plane surface — Event Grid models
// those as event subscriptions and a separate data-plane publish endpoint on
// the topic's own hostname — so this handler covers only the topic
// (event-bus) lifecycle. See the package README note in the New docstring for
// the honest scope.
//
// This handler claims Microsoft.EventGrid/topics only; it is disjoint from
// every other Azure ARM provider, so registration order relative to them is
// unconstrained. It must register before the permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../providers/Microsoft.EventGrid/topics/{t}   — Topics.CreateOrUpdate (LRO, completes inline)
//	GET    .../providers/Microsoft.EventGrid/topics/{t}   — Topics.Get
//	DELETE .../providers/Microsoft.EventGrid/topics/{t}   — Topics.Delete (LRO, completes inline)
//	GET    .../providers/Microsoft.EventGrid/topics       — Topics.ListBySubscription / ListByResourceGroup
package eventgrid

import (
	"net/http"

	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName = "Microsoft.EventGrid"
	typeTopics   = "topics"
)

// Handler serves Microsoft.EventGrid/topics ARM requests against an eventbus
// driver.
type Handler struct {
	bus ebdriver.EventBus
}

// New returns an Azure Event Grid handler backed by b.
func New(b ebdriver.EventBus) *Handler {
	return &Handler{bus: b}
}

// Matches claims ARM URLs targeting Microsoft.EventGrid/topics. Disjoint from
// every other Azure ARM provider, so registration order is unconstrained.
// Registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == typeTopics
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no topic name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listTopics(w, r, &rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateTopic(w, r, &rp)
	case http.MethodGet:
		h.getTopic(w, r, &rp)
	case http.MethodDelete:
		h.deleteTopic(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
