// Package servicebus serves Azure Service Bus ARM control-plane requests
// (Microsoft.ServiceBus/namespaces and their queues, topics, subscriptions,
// rules, and authorization rules) plus a raw-HTTP data plane for
// send/receive/peek-lock against a CloudEmu messagequeue driver.
//
// Real azure-sdk-for-go armservicebus clients drive the ARM control plane.
// The data-plane azservicebus SDK uses AMQP exclusively and is out of scope;
// tests that exercise send/receive hit the REST data plane directly with raw
// HTTP, mirroring Microsoft's "Send/Receive REST" endpoints documented at
// https://learn.microsoft.com/rest/api/servicebus/. The REST data plane
// addresses both flat queues (/{queue}/messages...) and topic subscriptions
// (/{topic}/subscriptions/{sub}/messages...): a publish to a topic fans the
// message out to every subscription's backing store, and each subscription is
// received from independently.
//
// Namespaces own their child entities: queues, topics (which own
// subscriptions, which own rules) and authorization rules. Every namespace is
// created with a default RootManageSharedAccessKey authorization rule so a
// client can obtain a connection string via listKeys. Deleting a namespace
// cascades to all of its child entities.
package servicebus

import (
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	providerName = "Microsoft.ServiceBus"
	resourceType = "namespaces"

	segQueues     = "queues"
	segTopics     = "topics"
	segSubs       = "subscriptions"
	segRules      = "rules"
	segAuthRules  = "authorizationRules"
	actionKeys    = "listKeys"
	actionRegen   = "regenerateKeys"
	checkNamePath = "checkNameAvailability"

	maxBodyBytes  = 1 << 20
	dataPlanePath = "/messages"

	sbHost = ".servicebus.windows.net"
)

// Handler serves ARM Service Bus + raw-HTTP data-plane requests.
type Handler struct {
	mq         mqdriver.MessageQueue
	mu         sync.RWMutex
	namespaces *memstore.Store[*namespaceState]
}

// New returns a Service Bus handler backed by mq for message storage.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{mq: mq, namespaces: memstore.New[*namespaceState]()}
}

// sbPath is a parsed Service Bus ARM URL. segs holds the path segments that
// follow the namespace name (e.g. ["queues","orders"] or
// ["topics","t","subscriptions","s","rules","r"]). It is kept under the
// gocritic hugeParam threshold so it can be passed by value across dispatch.
type sbPath struct {
	sub       string
	rg        string
	namespace string
	segs      []string
}

// pathKind classifies a parsed Service Bus path.
type pathKind int

const (
	kindResource pathKind = iota
	kindCheckName
)

// parseSBPath parses a Service Bus ARM path. ok is false for non-Service-Bus
// paths and for malformed provider paths.
func parseSBPath(urlPath string) (sbPath, pathKind, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	providerIdx, ok := findProvider(parts)
	if !ok {
		return sbPath{}, kindResource, false
	}

	// Only the segments BEFORE providers carry the ARM scope. The trailing
	// "subscriptions/{name}" child of a topic must not be mistaken for the
	// ARM subscription id.
	sp := parseScope(parts, providerIdx)

	rest := parts[providerIdx+namePairLen:]
	if len(rest) > 0 && strings.EqualFold(rest[0], checkNamePath) {
		return sp, kindCheckName, true
	}

	if len(rest) == 0 || !strings.EqualFold(rest[0], resourceType) {
		return sbPath{}, kindResource, false
	}

	if len(rest) >= namePairLen {
		sp.namespace = rest[1]
		sp.segs = rest[namePairLen:]
	}

	return sp, kindResource, true
}

// namePairLen is the length of a {keyword}/{value} pair such as
// namespaces/{ns}.
const namePairLen = 2

// findProvider returns the index of the "providers" segment when it is
// immediately followed by the Service Bus provider name.
func findProvider(parts []string) (int, bool) {
	for i, s := range parts {
		if !strings.EqualFold(s, "providers") {
			continue
		}

		if i+1 < len(parts) && strings.EqualFold(parts[i+1], providerName) {
			return i, true
		}

		return 0, false
	}

	return 0, false
}

// parseScope reads the subscription and resource group from the segments that
// precede the providers segment.
func parseScope(parts []string, providerIdx int) sbPath {
	sp := sbPath{}

	for i := 0; i+1 < providerIdx; i++ {
		switch {
		case strings.EqualFold(parts[i], "subscriptions"):
			sp.sub = parts[i+1]
		case strings.EqualFold(parts[i], "resourceGroups"):
			sp.rg = parts[i+1]
		}
	}

	return sp
}

// Matches accepts Service Bus ARM paths plus data-plane URLs ending in
// /messages or /messages/head.
//
// The flat "/{entity}/messages" data-plane shape is NOT unique to Service Bus:
// Azure Queue Storage's azqueue SDK addresses "/{queue}/messages" the same way.
// Real Azure separates the two by hostname (*.servicebus.windows.net vs
// *.queue.core.windows.net), which is unavailable behind CloudEmu's shared
// endpoint. To keep the Queue Storage message plane alive, Service Bus claims a
// flat "/{entity}/messages" request ONLY when {entity} resolves to a Service
// Bus queue or topic it actually holds; otherwise it declines so the request
// falls through to the Queue Storage handler (registered after this one).
// Subscription-addressed paths ("/{topic}/subscriptions/{sub}/messages") have
// no Queue Storage counterpart, so they are always Service Bus's to serve.
func (h *Handler) Matches(r *http.Request) bool {
	if isDataPlanePath(r.URL.Path) {
		return h.claimsDataPlane(r.URL.Path)
	}

	_, _, ok := parseSBPath(r.URL.Path)

	return ok
}

// claimsDataPlane reports whether a data-plane URL addresses a Service Bus
// entity this handler holds. A subscription path is unambiguously Service Bus.
// A flat "/{entity}/messages" path (which Queue Storage's azqueue SDK shares)
// is claimed only when the entity resolves to a known Service Bus queue or
// topic; an unknown entity is declined so Queue Storage can serve it.
func (h *Handler) claimsDataPlane(p string) bool {
	tgt, parsed := parseDataPlanePath(p)
	if !parsed || tgt.entity == "" {
		return false
	}

	if tgt.sub != "" {
		// A "/{topic}/subscriptions/{sub}/messages" request is Service Bus's to
		// serve whenever the parent topic exists (Queue Storage has no such
		// shape). serveSubscriptionData then 404s a missing subscription.
		_, ok := h.resolveTopicSubURLs(tgt.namespace, tgt.entity)

		return ok
	}

	if _, ok := h.resolveQueue(tgt.namespace, tgt.entity); ok {
		return true
	}

	_, isTopic := h.resolveTopicSubURLs(tgt.namespace, tgt.entity)

	return isTopic
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isDataPlanePath(r.URL.Path) {
		h.serveDataPlane(w, r)
		return
	}

	sp, kind, ok := parseSBPath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch {
	case kind == kindCheckName:
		h.checkNameAvailability(w, r)
	case sp.namespace == "":
		h.listNamespaces(w, r, sp)
	case len(sp.segs) == 0:
		h.serveNamespace(w, r, sp)
	default:
		h.serveChild(w, r, sp)
	}
}

// serveChild dispatches the child-entity routes under a namespace.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, sp sbPath) {
	switch {
	case strings.EqualFold(sp.segs[0], segQueues):
		h.serveQueue(w, r, sp)
	case strings.EqualFold(sp.segs[0], segTopics):
		h.serveTopicTree(w, r, sp)
	case strings.EqualFold(sp.segs[0], segAuthRules):
		h.serveAuthRule(w, r, sp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"unsupported sub-resource: "+sp.segs[0])
	}
}

// listChildren renders a namespace-scoped child collection: it enforces GET,
// resolves the namespace (404 when absent), and paginates whatever collect
// returns. collect runs under the read lock.
func (h *Handler) listChildren(w http.ResponseWriter, r *http.Request, sp sbPath,
	collect func(*namespaceState) []any) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.RUnlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	resources := collect(ns)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
}

// nsKey normalizes a namespace name to its store key. Service Bus namespace
// names map 1:1 to a DNS host (<name>.servicebus.windows.net) and real Azure
// treats them case-insensitively for uniqueness and lookup.
func nsKey(name string) string { return strings.ToLower(name) }

// getNS returns the namespace state if it exists and matches the request scope.
func (h *Handler) getNS(sp sbPath) (*namespaceState, bool) {
	ns, ok := h.namespaces.Get(nsKey(sp.namespace))
	if !ok {
		return nil, false
	}

	if !strings.EqualFold(ns.Subscription, sp.sub) || !strings.EqualFold(ns.ResourceGroup, sp.rg) {
		return nil, false
	}

	return ns, true
}
