// Package eventgrid implements the Azure Event Grid
// (Microsoft.EventGrid/topics) ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com, driving the shared eventbus driver.
//
// Event Grid topics map onto the eventbus driver's event buses: a topic is an
// event bus keyed by its user-assigned name. Event subscriptions map onto the
// driver's rules (the raw ARM properties round-trip verbatim), and the
// data-plane publish endpoint is served separately by PublishHandler.
//
// This handler claims Microsoft.EventGrid/topics only; it is disjoint from
// every other Azure ARM provider, so registration order relative to them is
// unconstrained. It must register before the permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT/GET/DELETE .../topics/{t}                           — Topics CRUD (LRO, completes inline)
//	GET            .../topics                               — Topics.ListBySubscription / ListByResourceGroup
//	POST           .../topics/{t}/listKeys                  — Topics.ListSharedAccessKeys
//	PUT/GET/DELETE .../topics/{t}/eventSubscriptions/{s}    — TopicEventSubscriptions CRUD
//	GET            .../topics/{t}/eventSubscriptions        — TopicEventSubscriptions.List
package eventgrid

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
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

	switch rp.SubResource {
	case actionListKeys:
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w)
			return
		}

		h.listTopicKeys(w, r, &rp)
	case subEventSubscriptions:
		h.serveEventSubscription(w, r, &rp)
	case "":
		h.serveTopic(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported Event Grid topic sub-resource")
	}
}

func (h *Handler) serveTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateTopic(w, r, rp)
	case http.MethodGet:
		h.getTopic(w, r, rp)
	case http.MethodDelete:
		h.deleteTopic(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveEventSubscription routes .../topics/{t}/eventSubscriptions[/{name}].
func (h *Handler) serveEventSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listEventSubscriptions(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateEventSubscription(w, r, rp)
	case http.MethodGet:
		h.getEventSubscription(w, r, rp)
	case http.MethodDelete:
		h.deleteEventSubscription(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
