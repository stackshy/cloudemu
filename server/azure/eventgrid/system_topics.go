package eventgrid

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	systemTopicResourceType         = "Microsoft.EventGrid/systemTopics"
	systemTopicSubscriptionType     = "Microsoft.EventGrid/systemTopics/eventSubscriptions"
	defaultSystemTopicLocation      = "global"
	systemTopicProvisioningState    = "Succeeded"
	systemTopicSubProvisioningState = "Succeeded"
)

// systemTopicRecord is the wire-handler-owned state for one system topic. A
// system topic represents an Azure resource (the source) that emits events into
// Event Grid; the emulator models the management surface (CRUD + its event
// subscriptions) rather than the external source itself.
type systemTopicRecord struct {
	name          string
	location      string
	source        string
	topicType     string
	sub           string
	rg            string
	tags          map[string]string
	subscriptions map[string]json.RawMessage
}

// systemTopicJSON is the ARM SystemTopic resource shape (armeventgrid.SystemTopic).
type systemTopicJSON struct {
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Location   string                 `json:"location"`
	Tags       map[string]*string     `json:"tags,omitempty"`
	Properties *systemTopicProperties `json:"properties,omitempty"`
}

type systemTopicProperties struct {
	Source            string `json:"source,omitempty"`
	TopicType         string `json:"topicType,omitempty"`
	MetricResourceID  string `json:"metricResourceId,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}

type systemTopicListResult struct {
	Value []systemTopicJSON `json:"value"`
}

// storeKey scopes a resource record by subscription + resource group + name so
// same-named resources in different groups do not collide.
func storeKey(sub, rg, name string) string {
	return sub + "|" + rg + "|" + name
}

func systemTopicID(rp *azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeSystemTopics, rp.ResourceName)
}

func (rec *systemTopicRecord) toJSON(rp *azurearm.ResourcePath) systemTopicJSON {
	id := systemTopicID(rp)

	return systemTopicJSON{
		ID:       id,
		Name:     rec.name,
		Type:     systemTopicResourceType,
		Location: rec.location,
		Tags:     tagsToPtr(rec.tags),
		Properties: &systemTopicProperties{
			Source:            rec.source,
			TopicType:         rec.topicType,
			MetricResourceID:  id,
			ProvisioningState: systemTopicProvisioningState,
		},
	}
}

// serveSystemTopics routes .../systemTopics[/{name}[/eventSubscriptions[/{sub}]]].
func (h *Handler) serveSystemTopics(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listSystemTopics(w, rp)

		return
	}

	switch rp.SubResource {
	case "":
		h.serveSystemTopicResource(w, r, rp)
	case subEventSubscriptions:
		h.serveSystemTopicSubscription(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported system topic sub-resource")
	}
}

func (h *Handler) serveSystemTopicResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateSystemTopic(w, r, rp)
	case http.MethodPatch:
		h.updateSystemTopic(w, r, rp)
	case http.MethodGet:
		h.getSystemTopic(w, rp)
	case http.MethodDelete:
		h.deleteSystemTopic(w, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createOrUpdateSystemTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body systemTopicJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	loc := body.Location
	if loc == "" {
		loc = defaultSystemTopicLocation
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.systemTopics[key]
	if rec == nil {
		rec = &systemTopicRecord{
			name:          rp.ResourceName,
			sub:           rp.Subscription,
			rg:            rp.ResourceGroup,
			subscriptions: make(map[string]json.RawMessage),
		}
		h.systemTopics[key] = rec
	}

	rec.location = loc
	rec.tags = tagsFromPtr(body.Tags)

	if body.Properties != nil {
		rec.source = body.Properties.Source
		rec.topicType = body.Properties.TopicType
	}

	out := rec.toJSON(rp)
	h.mu.Unlock()

	// 201 with a terminal provisioningState completes the SDK's LRO poller on
	// the first response.
	azurearm.WriteJSON(w, http.StatusCreated, out)
}

// systemTopicUpdateJSON is the SystemTopics.Update (PATCH) request body: mutable
// tags. Identity and source are not changed here.
type systemTopicUpdateJSON struct {
	Tags map[string]*string `json:"tags,omitempty"`
}

// updateSystemTopic maps SystemTopics.Update (PATCH) onto the wire-owned record:
// it merges the supplied tags onto the existing tags, preserving any the caller
// omitted, and returns the updated system topic (200). 404 when the system topic
// does not exist, before any write.
func (h *Handler) updateSystemTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body systemTopicUpdateJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.systemTopics[key]
	if rec == nil {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "system topic not found")

		return
	}

	if body.Tags != nil {
		rec.tags = mergeTags(rec.tags, tagsFromPtr(body.Tags))
	}

	out := rec.toJSON(rp)
	h.mu.Unlock()

	// 201 with a terminal provisioningState completes the SDK's Update LRO
	// poller on the first response (the poller accepts 200 or 201).
	azurearm.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) getSystemTopic(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()
	rec := h.systemTopics[key]

	if rec == nil {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "system topic not found")

		return
	}

	out := rec.toJSON(rp)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteSystemTopic(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()
	_, found := h.systemTopics[key]
	delete(h.systemTopics, key)
	h.mu.Unlock()

	if !found {
		// ARM delete is idempotent: a delete of a missing resource completes 204.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listSystemTopics(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	h.mu.RLock()
	recs := recordsInScope(h.systemTopics, rp.Subscription, rp.ResourceGroup)
	out := scopedList(recs, rp, (*systemTopicRecord).toJSON)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, systemTopicListResult{Value: out})
}
