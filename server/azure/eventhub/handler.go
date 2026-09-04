// Package eventhub serves Azure Event Hubs ARM control-plane requests
// (Microsoft.EventHub/namespaces and their event hubs, consumer groups and
// authorization rules).
//
// Real azure-sdk-for-go armeventhub clients drive this surface. Namespaces own
// their child entities: event hubs (which own consumer groups and event-hub
// authorization rules) and namespace authorization rules. Every namespace is
// created with a default RootManageSharedAccessKey authorization rule so a
// client can obtain a connection string via listKeys, and every event hub is
// created with the built-in $Default consumer group. Deleting a namespace
// cascades to all of its child entities.
//
// Event Hubs has no ARM-reachable data plane: sending and receiving events is
// AMQP/Kafka only. This handler is therefore control-plane only, exactly like
// the Service Bus ARM handler.
package eventhub

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.EventHub"
	resourceType = "namespaces"

	segEventHubs      = "eventhubs"
	segConsumerGroups = "consumergroups"
	segAuthRules      = "authorizationRules"
	actionKeys        = "listKeys"
	actionRegen       = "regenerateKeys"
	checkNamePath     = "checkNameAvailability"

	maxBodyBytes = 1 << 20

	// namePairLen is the length of a {keyword}/{value} pair such as
	// namespaces/{ns}.
	namePairLen = 2
)

// Handler serves ARM Event Hubs requests.
type Handler struct {
	mu         sync.RWMutex
	namespaces *memstore.Store[*namespaceState]
}

// New returns an Event Hubs control-plane handler.
func New() *Handler {
	return &Handler{namespaces: memstore.New[*namespaceState]()}
}

// ehPath is a parsed Event Hubs ARM URL. segs holds the path segments that
// follow the namespace name (e.g. ["eventhubs","orders"] or
// ["eventhubs","orders","consumergroups","cg"]).
type ehPath struct {
	sub       string
	rg        string
	namespace string
	segs      []string
}

// pathKind classifies a parsed Event Hubs path.
type pathKind int

const (
	kindResource pathKind = iota
	kindCheckName
)

// parseEHPath parses an Event Hubs ARM path. ok is false for non-Event-Hubs
// paths and for malformed provider paths.
func parseEHPath(urlPath string) (ehPath, pathKind, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	providerIdx, ok := findProvider(parts)
	if !ok {
		return ehPath{}, kindResource, false
	}

	ep := parseScope(parts, providerIdx)

	rest := parts[providerIdx+namePairLen:]
	if len(rest) > 0 && strings.EqualFold(rest[0], checkNamePath) {
		return ep, kindCheckName, true
	}

	if len(rest) == 0 || !strings.EqualFold(rest[0], resourceType) {
		return ehPath{}, kindResource, false
	}

	if len(rest) >= namePairLen {
		ep.namespace = rest[1]
		ep.segs = rest[namePairLen:]
	}

	return ep, kindResource, true
}

// findProvider returns the index of the "providers" segment when it is
// immediately followed by the Event Hubs provider name.
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
func parseScope(parts []string, providerIdx int) ehPath {
	ep := ehPath{}

	for i := 0; i+1 < providerIdx; i++ {
		switch {
		case strings.EqualFold(parts[i], "subscriptions"):
			ep.sub = parts[i+1]
		case strings.EqualFold(parts[i], "resourceGroups"):
			ep.rg = parts[i+1]
		}
	}

	return ep
}

// Matches accepts Event Hubs ARM paths.
func (*Handler) Matches(r *http.Request) bool {
	_, _, ok := parseEHPath(r.URL.Path)

	return ok
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ep, kind, ok := parseEHPath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch {
	case kind == kindCheckName:
		h.checkNameAvailability(w, r)
	case ep.namespace == "":
		h.listNamespaces(w, r, ep)
	case len(ep.segs) == 0:
		h.serveNamespace(w, r, ep)
	default:
		h.serveChild(w, r, ep)
	}
}

// serveChild dispatches the child-entity routes under a namespace.
//
// Only the namespace exposes an ARM PATCH (Namespaces - Update). Real Azure has
// no Update/PATCH operation for the child entities — event hubs, consumer groups
// and authorization rules are mutated exclusively through Create Or Update (PUT)
// — so PATCH is intentionally not routed for them and falls through to the
// method-not-allowed default, matching the real control plane.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, ep ehPath) {
	switch {
	case eq(ep.segs[0], segEventHubs):
		h.serveEventHubTree(w, r, ep)
	case eq(ep.segs[0], segAuthRules):
		h.authRuleDispatch(w, r, ep.segs[1:], func() (authTarget, bool) { return h.nsAuthTargetLocked(ep) })
	default:
		notImplemented(w)
	}
}

func notImplemented(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "unsupported path")
}

func eq(a, b string) bool { return strings.EqualFold(a, b) }

// nsKey normalizes a namespace name to its store key; real Azure treats
// namespace names (which map 1:1 to a DNS host) case-insensitively.
func nsKey(name string) string { return strings.ToLower(name) }

// getNS returns the namespace state if it exists and matches the request scope.
// Callers hold h.mu.
func (h *Handler) getNS(ep ehPath) (*namespaceState, bool) {
	ns, ok := h.namespaces.Get(nsKey(ep.namespace))
	if !ok {
		return nil, false
	}

	if !strings.EqualFold(ns.Subscription, ep.sub) || !strings.EqualFold(ns.ResourceGroup, ep.rg) {
		return nil, false
	}

	return ns, true
}

func writeNSNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "namespace not found: "+name)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// paginate returns the listPageSize-sized window of resources that starts at the
// request's $skip offset, emitting a nextLink when more items remain.
func paginate(r *http.Request, resources []any) listResponse {
	skip := paginationSkip(r)
	if skip >= len(resources) {
		return listResponse{Value: []any{}}
	}

	end := skip + listPageSize
	if end >= len(resources) {
		return listResponse{Value: resources[skip:]}
	}

	return listResponse{Value: resources[skip:end], NextLink: nextPageLink(r, end)}
}

func paginationSkip(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get(skipParam))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// nextPageLink builds the absolute URL that continues a listing at offset skip,
// preserving the request path and query and overriding $skip.
func nextPageLink(r *http.Request, skip int) string {
	next := *r.URL
	next.Host = r.Host

	next.Scheme = "http"
	if r.TLS != nil {
		next.Scheme = "https"
	}

	q := next.Query()
	q.Set(skipParam, strconv.Itoa(skip))
	next.RawQuery = q.Encode()

	return next.String()
}
