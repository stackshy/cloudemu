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
//	PATCH          .../topics/{t}                           — Topics.Update (merge tags + mutable properties)
//	GET            .../topics                               — Topics.ListBySubscription / ListByResourceGroup
//	POST           .../topics/{t}/listKeys                  — Topics.ListSharedAccessKeys
//	POST           .../topics/{t}/regenerateKey            — Topics.RegenerateKey
//	PUT/GET/DELETE .../topics/{t}/eventSubscriptions/{s}    — TopicEventSubscriptions CRUD
//	GET            .../topics/{t}/eventSubscriptions        — TopicEventSubscriptions.List
//	PUT/GET/DELETE .../domains/{d}/topics/{t}               — DomainTopics CRUD
//	GET            .../domains/{d}/topics                   — DomainTopics.ListByDomain
//	PUT/GET/DELETE {scope}/.../eventSubscriptions/{s}      — EventSubscriptions CRUD (subscription/RG/resource scope)
//	GET            {scope}/.../eventSubscriptions          — EventSubscriptions List (ByResource / Global{BySub,ByRG})
package eventgrid

import (
	"net/http"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	providerName     = "Microsoft.EventGrid"
	typeTopics       = "topics"
	typeSystemTopics = "systemTopics"
	typeDomains      = "domains"
)

// Handler serves Microsoft.EventGrid ARM requests. Topics (and their event
// subscriptions) are backed by the eventbus driver. System topics and domains
// are Event-Grid-only ARM resources with no generic driver equivalent (a system
// topic wraps an external Azure event source; a domain groups topics and holds
// its own access keys), so — mirroring the Cosmos /offers precedent in this
// codebase — the wire handler owns their state in memory.
type Handler struct {
	bus ebdriver.EventBus

	mu           sync.RWMutex
	systemTopics map[string]*systemTopicRecord
	domains      map[string]*domainRecord
	// scopedSubs holds event subscriptions created as extension resources on a
	// non-topic scope (subscription, resource group, or an arbitrary resource).
	// These have no eventbus-driver topic to hang off, so — like systemTopics
	// and domains — the wire handler owns their state. Keyed by scope+name.
	scopedSubs map[string]*scopedSubRecord
	// topicKeyGens holds the per-topic shared-access-key generation counters.
	// A topic's keys are derived deterministically from its name plus a
	// per-key generation; RegenerateKey bumps the requested key's generation so
	// the rotated key differs while the other is left untouched. Keyed by
	// scope+name; a missing entry means both keys are at generation zero.
	topicKeyGens map[string]*topicKeyGens
}

// topicKeyGens tracks the generation counter of each of a topic's two shared
// access keys. Bumping a counter rotates only that key.
type topicKeyGens struct {
	key1Gen int
	key2Gen int
}

// New returns an Azure Event Grid handler backed by b.
func New(b ebdriver.EventBus) *Handler {
	return &Handler{
		bus:          b,
		systemTopics: make(map[string]*systemTopicRecord),
		domains:      make(map[string]*domainRecord),
		scopedSubs:   make(map[string]*scopedSubRecord),
		topicKeyGens: make(map[string]*topicKeyGens),
	}
}

// Matches claims ARM URLs targeting Microsoft.EventGrid topics, systemTopics,
// and domains. Disjoint from every other Azure ARM provider, so registration
// order is unconstrained. Registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	// Scope-bound event subscriptions are an extension resource that can hang
	// off any scope (subscription, resource group, or an arbitrary resource of
	// a different provider), so the outer provider in the path is not
	// necessarily Microsoft.EventGrid. Claim them by the trailing marker.
	if _, ok := parseScopedEventSubscription(r.URL.Path); ok {
		return true
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rp.Provider != providerName {
		return false
	}

	switch rp.ResourceType {
	case typeTopics, typeSystemTopics, typeDomains:
		return true
	default:
		return false
	}
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Scope-bound / extension-form event subscriptions are recognized ahead of
	// the topic-oriented parse: their path carries an extra
	// providers/Microsoft.EventGrid segment that azurearm.ParsePath cannot model.
	if sp, ok := parseScopedEventSubscription(r.URL.Path); ok {
		h.serveScopedEventSubscription(w, r, &sp)
		return
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch rp.ResourceType {
	case typeSystemTopics:
		h.serveSystemTopics(w, r, &rp)
		return
	case typeDomains:
		h.serveDomains(w, r, &rp)
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

	h.serveTopicResource(w, r, &rp)
}

// serveTopicResource routes a named topic and its sub-resources.
func (h *Handler) serveTopicResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch rp.SubResource {
	case actionListKeys:
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w)
			return
		}

		h.listTopicKeys(w, r, rp)
	case subActionRegenerateKey:
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w)
			return
		}

		h.regenerateTopicKey(w, r, rp)
	case subEventSubscriptions:
		h.serveEventSubscription(w, r, rp)
	case "":
		h.serveTopic(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported Event Grid topic sub-resource")
	}
}

func (h *Handler) serveTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateTopic(w, r, rp)
	case http.MethodPatch:
		h.updateTopic(w, r, rp)
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
	case http.MethodPatch:
		h.updateEventSubscription(w, r, rp)
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
