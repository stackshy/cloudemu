package notifications

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"

	notifprovider "github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Sort keys ListTopics accepts.
const (
	sortByTimeCreated    = "TIMECREATED"
	sortByLifecycleState = "LIFECYCLESTATE"

	sortOrderAsc  = "ASC"
	sortOrderDesc = "DESC"
)

// entityTopic is the resource type a topic work request reports.
const entityTopic = "onstopic"

// Work request operations the asynchronous topic mutations record.
const (
	operationDeleteTopic       = "DELETE_TOPIC"
	operationChangeCompartment = "CHANGE_TOPIC_COMPARTMENT"
)

// serveTopics routes the topic collection, a single topic, its message
// endpoint and its compartment move.
func (h *Handler) serveTopics(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.ID == "" && rt.Sub == "":
		h.serveTopicCollection(w, r)
	case rt.Sub == "":
		h.serveTopic(w, r, rt.ID)
	case rt.Sub == subMessages && rt.Action == "":
		h.publishMessage(w, r, rt.ID)
	case rt.Sub == subActions && rt.Action == actionChangeCompartment:
		h.changeTopicCompartment(w, r, rt.ID)
	default:
		notFound(w, r)
	}
}

func (h *Handler) serveTopicCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createTopic(w, r)
	case http.MethodGet:
		h.listTopics(w, r)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) serveTopic(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getTopic(w, r, id)
	case http.MethodPut:
		h.updateTopic(w, r, id)
	case http.MethodDelete:
		h.deleteTopic(w, r, id)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request) {
	var req createTopicRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !refuseDefinedTags(w, r, req.DefinedTags) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	info, err := h.notif.CreateTopic(r.Context(), notifdriver.TopicConfig{
		Name:        req.Name,
		DisplayName: req.Description,
		Tags:        req.FreeformTags,
		Scope:       scope.Scope{Compartment: req.CompartmentID},
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.topicWire(r, info))
}

func (h *Handler) getTopic(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.notif.GetTopic(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.topicWire(r, info))
}

// listTopics returns the topics in a compartment. ONS requires compartmentId
// and offers id, name and lifecycleState narrowing on top of it.
func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	order, ok := sortSpec(w, r)
	if !ok {
		return
	}

	infos, err := h.notif.ListTopics(r.Context(), scope.Scope{Compartment: compartmentID})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	query := r.URL.Query()
	out := make([]topicResponse, 0, len(infos))

	for i := range infos {
		topic := h.topicWire(r, &infos[i])
		if !topicMatches(&topic, query) {
			continue
		}

		out = append(out, topic)
	}

	sortTopics(out, order)

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

// updateTopic replaces a topic's description and tags. ONS does not rename a
// topic, so the stored name is carried through.
func (h *Handler) updateTopic(w http.ResponseWriter, r *http.Request, id string) {
	var req updateTopicRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if !refuseDefinedTags(w, r, req.DefinedTags) {
		return
	}

	info, err := h.notif.UpdateTopic(r.Context(), notifdriver.TopicConfig{
		Name:        id,
		DisplayName: req.Description,
		Tags:        req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.topicWire(r, info))
}

// deleteTopic removes a topic and its subscriptions. Real ONS runs it
// asynchronously and answers 204 with the work request the caller polls.
func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, id string) {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	info, err := h.notif.GetTopic(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	compartmentID := info.Scope.Compartment

	if err := h.notif.DeleteTopic(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	wrID := h.work.Accept(operationDeleteTopic, compartmentID, workrequest.Resource{
		EntityType: entityTopic,
		ActionType: workrequest.ActionDeleted,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// changeTopicCompartment moves a topic, which ONS runs asynchronously.
func (h *Handler) changeTopicCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if _, err := h.notif.UpdateTopic(r.Context(), notifdriver.TopicConfig{
		Name:  id,
		Scope: scope.Scope{Compartment: req.CompartmentID},
	}); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	wrID := h.work.Accept(operationChangeCompartment, req.CompartmentID, workrequest.Resource{
		EntityType: entityTopic,
		ActionType: workrequest.ActionUpdated,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// publishMessage is the ONS data plane: a message posted to the topic's own
// endpoint, which CloudEmu serves on the same listener as the control plane.
func (h *Handler) publishMessage(w http.ResponseWriter, r *http.Request, topicID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req messageDetails

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	msg, err := h.extras.PublishMessage(r.Context(), topicID, notifprovider.MessageSpec{
		Title: req.Title,
		Body:  req.Body,
		Type:  r.URL.Query().Get("messageType"),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, publishResult{MessageID: msg.ID, TimeStamp: msg.Timestamp})
}

// topicWire renders a topic, folding in the OCI-only state the portable
// projection has no room for.
func (h *Handler) topicWire(r *http.Request, info *notifdriver.TopicInfo) topicResponse {
	out := topicResponse{
		TopicID:        info.ID,
		Name:           info.Name,
		CompartmentID:  info.Scope.Compartment,
		APIEndpoint:    apiEndpoint(r),
		LifecycleState: notifprovider.StateActive,
		Description:    info.DisplayName,
		FreeformTags:   info.Tags,
		DefinedTags:    definedTags{},
	}

	if out.FreeformTags == nil {
		out.FreeformTags = map[string]string{}
	}

	if details, ok := h.extras.TopicDetails(info.ID); ok {
		out.LifecycleState = details.LifecycleState
		out.TimeCreated = details.TimeCreated
		out.Etag = details.Etag
		out.ShortTopicID = details.ShortTopicID
	}

	return out
}

// topicMatches applies ONS's id, name and lifecycleState narrowing.
func topicMatches(topic *topicResponse, query url.Values) bool {
	if id := query.Get("id"); id != "" && topic.TopicID != id {
		return false
	}

	if name := query.Get("name"); name != "" && topic.Name != name {
		return false
	}

	if state := query.Get("lifecycleState"); state != "" && topic.LifecycleState != state {
		return false
	}

	return true
}

// sortSpec reads ONS's sortBy and sortOrder, refusing a key this handler does
// not order on rather than returning an arbitrary order under its name.
func sortSpec(w http.ResponseWriter, r *http.Request) (order [2]string, ok bool) {
	query := r.URL.Query()
	by, dir := query.Get("sortBy"), query.Get("sortOrder")

	if by != "" && by != sortByTimeCreated && by != sortByLifecycleState {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"sortBy must be "+sortByTimeCreated+" or "+sortByLifecycleState)

		return order, false
	}

	if dir != "" && dir != sortOrderAsc && dir != sortOrderDesc {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"sortOrder must be "+sortOrderAsc+" or "+sortOrderDesc)

		return order, false
	}

	return [2]string{by, dir}, true
}

// sortTopics applies the caller's sortBy and sortOrder to a listing.
func sortTopics(topics []topicResponse, order [2]string) {
	if order[0] == "" && order[1] == "" {
		return
	}

	less := func(i, j int) bool { return topics[i].TimeCreated < topics[j].TimeCreated }
	if order[0] == sortByLifecycleState {
		less = func(i, j int) bool { return topics[i].LifecycleState < topics[j].LifecycleState }
	}

	sort.SliceStable(topics, less)

	if order[1] == sortOrderDesc {
		reverse(topics)
	}
}

func reverse[T any](items []T) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

// paginate applies OCI's limit and opaque page cursor, stamping the cursor for
// the next page. The cursor is the offset the next page starts at.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	start := 0

	if token := ocirest.Page(r); token != "" {
		if n, err := strconv.Atoi(token); err == nil && n > 0 {
			start = n
		}
	}

	// items[:0] rather than nil: an empty page is [] on the wire, not null.
	if start >= len(items) {
		return items[:0]
	}

	end := min(start+ocirest.Limit(r), len(items))
	if end < len(items) {
		ocirest.SetNextPage(w, strconv.Itoa(end))
	}

	return items[start:end]
}
