package eventgrid

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	// scopedSubscriptionResourceType is the ARM type of an event subscription
	// created as an extension resource on a non-topic scope.
	scopedSubscriptionResourceType = "Microsoft.EventGrid/eventSubscriptions"
	segProviders                   = "providers"
	segSubscriptions               = "subscriptions"
)

// scopedSubPath is a parsed scope-bound event subscription URL of the form
// {scope}/providers/Microsoft.EventGrid/eventSubscriptions[/{name}], where
// {scope} is a subscription, resource group, or an arbitrary resource id.
type scopedSubPath struct {
	scope         string // full ARM scope, leading slash, no trailing marker
	name          string // subscription name; empty for a list request
	subscription  string
	resourceGroup string
	topicName     string // set only when {scope} is a Microsoft.EventGrid topic
}

// scopedSubRecord is a wire-handler-owned event subscription attached to a
// non-topic scope. It has no eventbus-driver topic to hang off, so — like
// systemTopics and domains — the handler owns its state; the raw ARM
// properties round-trip verbatim.
type scopedSubRecord struct {
	scope         string
	name          string
	subscription  string
	resourceGroup string
	properties    json.RawMessage
}

// scopedSubMarker returns the index of the "providers" segment of the trailing
// providers/Microsoft.EventGrid/eventSubscriptions marker, or -1 when absent.
// The scan runs back-to-front so the last (extension) marker wins even when the
// scope itself is a Microsoft.EventGrid resource.
func scopedSubMarker(parts []string) int {
	for i := len(parts) - 3; i >= 0; i-- {
		if parts[i] == segProviders && parts[i+1] == providerName && parts[i+2] == subEventSubscriptions {
			return i
		}
	}

	return -1
}

// parseScopedEventSubscription recognizes a scope-bound event subscription path.
// azurearm.ParsePath cannot model it because the nested provider segment pushes
// the real name past the fields it tracks.
func parseScopedEventSubscription(path string) (scopedSubPath, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")

	i := scopedSubMarker(parts)
	if i < 0 {
		return scopedSubPath{}, false
	}

	scopeSegs := parts[:i]
	if len(scopeSegs) < 2 || scopeSegs[0] != segSubscriptions {
		return scopedSubPath{}, false
	}

	// Reject trailing action segments (getFullUrl, getDeliveryAttributes): the
	// name, if present, is the single segment right after the marker.
	if len(parts) > i+4 {
		return scopedSubPath{}, false
	}

	sp := scopedSubPath{scope: "/" + strings.Join(scopeSegs, "/")}
	if len(parts) > i+3 {
		sp.name = parts[i+3]
	}

	parseScopeContainers(scopeSegs, &sp)

	return sp, true
}

// parseScopeContainers pulls the subscription, resource group, and (when the
// scope is an Event Grid topic) the topic name out of the scope segments.
func parseScopeContainers(segs []string, sp *scopedSubPath) {
	if len(segs) >= 2 && segs[0] == segSubscriptions {
		sp.subscription = segs[1]
	}

	if len(segs) >= 4 && strings.EqualFold(segs[2], "resourceGroups") {
		sp.resourceGroup = segs[3]
	}

	n := len(segs)
	if n >= 4 && segs[n-4] == segProviders && segs[n-3] == providerName && segs[n-2] == typeTopics {
		sp.topicName = segs[n-1]
	}
}

func scopedSubKey(scope, name string) string {
	return scope + "\x00" + name
}

// serveScopedEventSubscription handles CRUD + list for scope-bound event
// subscriptions.
func (h *Handler) serveScopedEventSubscription(w http.ResponseWriter, r *http.Request, sp *scopedSubPath) {
	// A topic-scoped extension form is the same resource as the direct
	// .../topics/{t}/eventSubscriptions/{n} form, so route it through the
	// eventbus driver to unify with direct-form subs and share delivery.
	if sp.topicName != "" {
		rp := &azurearm.ResourcePath{
			Subscription:    sp.subscription,
			ResourceGroup:   sp.resourceGroup,
			Provider:        providerName,
			ResourceType:    typeTopics,
			ResourceName:    sp.topicName,
			SubResource:     subEventSubscriptions,
			SubResourceName: sp.name,
		}
		h.serveEventSubscription(w, r, rp)

		return
	}

	if sp.name == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listScopedEventSubscriptions(w, sp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putScopedEventSubscription(w, r, sp)
	case http.MethodGet:
		h.getScopedEventSubscription(w, sp)
	case http.MethodDelete:
		h.deleteScopedEventSubscription(w, sp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) putScopedEventSubscription(w http.ResponseWriter, r *http.Request, sp *scopedSubPath) {
	var body struct {
		Properties json.RawMessage `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	rec := &scopedSubRecord{
		scope:         sp.scope,
		name:          sp.name,
		subscription:  sp.subscription,
		resourceGroup: sp.resourceGroup,
		properties:    body.Properties,
	}

	h.mu.Lock()
	h.scopedSubs[scopedSubKey(sp.scope, sp.name)] = rec
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusCreated, toScopedSubJSON(rec))
}

func (h *Handler) getScopedEventSubscription(w http.ResponseWriter, sp *scopedSubPath) {
	h.mu.RLock()
	rec, ok := h.scopedSubs[scopedSubKey(sp.scope, sp.name)]
	h.mu.RUnlock()

	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "event subscription not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toScopedSubJSON(rec))
}

// deleteScopedEventSubscription removes the subscription. Delete is idempotent:
// a missing subscription returns 204, matching ARM (the SDK BeginDelete LRO
// accepts 200/202/204).
func (h *Handler) deleteScopedEventSubscription(w http.ResponseWriter, sp *scopedSubPath) {
	key := scopedSubKey(sp.scope, sp.name)

	h.mu.Lock()
	_, ok := h.scopedSubs[key]
	delete(h.scopedSubs, key)
	h.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listScopedEventSubscriptions(w http.ResponseWriter, sp *scopedSubPath) {
	h.mu.RLock()

	recs := make([]*scopedSubRecord, 0, len(h.scopedSubs))

	for _, rec := range h.scopedSubs {
		if scopedSubInListScope(rec, sp) {
			recs = append(recs, rec)
		}
	}

	h.mu.RUnlock()

	sort.Slice(recs, func(i, j int) bool { return recs[i].name < recs[j].name })

	out := make([]eventSubscriptionJSON, 0, len(recs))
	for _, rec := range recs {
		out = append(out, toScopedSubJSON(rec))
	}

	azurearm.WriteJSON(w, http.StatusOK, eventSubscriptionListResult{Value: out})
}

// scopedSubInListScope reports whether rec belongs in the list requested at sp.
// A resource-extension scope (containing a nested /providers/) matches exactly;
// a resource-group scope matches subscription+group; a subscription scope
// matches the subscription (ListGlobalBySubscription).
func scopedSubInListScope(rec *scopedSubRecord, sp *scopedSubPath) bool {
	if strings.Contains(sp.scope, "/providers/") {
		return rec.scope == sp.scope
	}

	if sp.resourceGroup != "" {
		return rec.subscription == sp.subscription && rec.resourceGroup == sp.resourceGroup
	}

	return rec.subscription == sp.subscription
}

func toScopedSubJSON(rec *scopedSubRecord) eventSubscriptionJSON {
	return eventSubscriptionJSON{
		ID:         rec.scope + "/providers/" + providerName + "/" + subEventSubscriptions + "/" + rec.name,
		Name:       rec.name,
		Type:       scopedSubscriptionResourceType,
		Properties: enrichSubscriptionProperties(rec.properties, rec.scope),
	}
}
