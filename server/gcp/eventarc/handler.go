// Package eventarc implements the eventarc.googleapis.com v1 REST API as a
// server.Handler. Real google.golang.org/api/eventarc/v1 clients pointed at
// this server create, get, list, and delete triggers end-to-end against the
// shared eventbus driver.
//
// Mapping (and what does NOT fit):
//
// Eventarc's public trigger API models a trigger as a filter → destination
// route; it has no first-class "event bus" in this surface (channels exist but
// only for third-party/partner sources and are not exercised by the trigger
// CRUD the SDK drives here). The eventbus driver, by contrast, requires a rule
// to live under an event bus, and its rule shape is (name, event-pattern,
// state, targets). We bridge the two by:
//
//   - Auto-provisioning one event bus per location, named "eventarc-<location>",
//     the first time a trigger is created there. This is a synthesized
//     container with no Eventarc analogue — the SDK never sees it.
//   - Mapping each trigger onto a driver rule keyed by the trigger id, with the
//     trigger's eventFilters serialized into the rule's EventPattern and the
//     destination folded into a single target so Get/List can round-trip them.
//
// Consequences of the impedance mismatch: Eventarc's rich destination types
// (Cloud Run, GKE, Cloud Functions, Workflows, HTTP), transport, channels, and
// conditions collapse onto the driver's flat Target/EventPattern strings, so
// only the subset the driver can store survives a round-trip. This is an honest
// approximation, not a faithful Eventarc emulation.
//
// This handler claims /v1/projects/{p}/locations/{l}/triggers[...] — a
// resource-type guard disjoint from the other /v1/projects/ GCP handlers
// (Firestore, IAM, Secret Manager, GKE, …), so registration order among them is
// unconstrained. Registered before the GCS fallback.
//
// Coverage (v1 REST):
//
//	POST   /v1/projects/{p}/locations/{l}/triggers?triggerId={id}   — Create (LRO, done inline)
//	GET    /v1/projects/{p}/locations/{l}/triggers/{id}             — Get
//	GET    /v1/projects/{p}/locations/{l}/triggers                  — List
//	DELETE /v1/projects/{p}/locations/{l}/triggers/{id}             — Delete (LRO, done inline)
package eventarc

import (
	"net/http"
	"strings"

	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

const (
	pathPrefix   = "/v1/projects/"
	locationsSeg = "locations"
	triggersSeg  = "triggers"
)

// minTriggersCollectionParts is the segment count of a triggers collection
// path: [projects, {p}, locations, {l}, triggers].
const minTriggersCollectionParts = 5

// Handler serves eventarc.googleapis.com v1 trigger requests against an
// eventbus driver.
type Handler struct {
	bus ebdriver.EventBus
}

// New returns an Eventarc handler backed by b.
func New(b ebdriver.EventBus) *Handler {
	return &Handler{bus: b}
}

type route struct {
	project  string
	location string
	trigger  string // trigger id; empty for the collection
}

// parseRoute extracts the components of an Eventarc v1 triggers path.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	// parts: [projects, {p}, locations, {l}, triggers, {id}?]
	if len(parts) < minTriggersCollectionParts ||
		parts[0] != "projects" || parts[2] != locationsSeg || parts[4] != triggersSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	if len(parts) > minTriggersCollectionParts {
		rt.trigger = parts[5]
	}

	return rt, true
}

// Matches claims Eventarc v1 trigger paths. Disjoint from the other
// /v1/projects/ GCP handlers, so registration order is unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

// ServeHTTP dispatches on method and path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed Eventarc v1 path")
		return
	}

	if rt.trigger == "" {
		switch r.Method {
		case http.MethodGet:
			h.listTriggers(w, r, &rt)
		case http.MethodPost:
			h.createTrigger(w, r, &rt)
		default:
			gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported triggers operation")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTrigger(w, r, &rt)
	case http.MethodDelete:
		h.deleteTrigger(w, r, &rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported trigger operation")
	}
}
