// Package clouddns implements the Cloud DNS (dns.googleapis.com) v1 REST API
// as a server.Handler. Real google.golang.org/api/dns/v1 clients pointed at
// this server CRUD managed zones, apply record changes, and list record sets
// end-to-end against the shared dns driver.
//
// Cloud DNS addresses managed zones by their user-assigned name in the URL
// (…/managedZones/{name}), while the dns driver keys zones on a generated
// zone-<uuid> id. The handler resolves the SDK-facing name to the driver id
// via a ListZones scan (resolveZoneID) so callers never see the internal id.
//
// Coverage (v1 REST):
//
//	POST   /dns/v1/projects/{p}/managedZones                        — Create zone
//	GET    /dns/v1/projects/{p}/managedZones/{z}                    — Get zone
//	GET    /dns/v1/projects/{p}/managedZones                        — List zones
//	DELETE /dns/v1/projects/{p}/managedZones/{z}                    — Delete zone
//	POST   /dns/v1/projects/{p}/managedZones/{z}/changes           — Apply record additions/deletions
//	GET    /dns/v1/projects/{p}/managedZones/{z}/rrsets            — List record sets
package clouddns

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

const (
	pathPrefix      = "/dns/v1/projects/"
	managedZonesSeg = "managedZones"
	changesSeg      = "changes"
	rrsetsSeg       = "rrsets"
)

// Path-tail segment counts after the [projects, {p}, managedZones] head.
const (
	minZonesCollectionParts = 3 // [projects, {p}, managedZones]
	restZoneResource        = 1 // [{zone}]
	restZoneSubCollection   = 2 // [{zone}, changes|rrsets]
)

// Handler serves dns.googleapis.com v1 requests against a dns driver.
type Handler struct {
	dns dnsdriver.DNS
}

// New returns a Cloud DNS handler backed by d.
func New(d dnsdriver.DNS) *Handler {
	return &Handler{dns: d}
}

type route struct {
	project string
	zone    string // managed zone name; empty for the collection
	sub     string // "changes" or "rrsets"; empty for a bare zone
}

// parseRoute extracts the components of a Cloud DNS v1 path.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/dns/v1/"), "/")
	if len(parts) < minZonesCollectionParts || parts[0] != "projects" || parts[2] != managedZonesSeg {
		return route{}, false
	}

	rt := route{project: parts[1]}
	rest := parts[minZonesCollectionParts:]

	switch len(rest) {
	case 0:
		return rt, true
	case restZoneResource:
		rt.zone = rest[0]
		return rt, true
	case restZoneSubCollection:
		rt.zone = rest[0]
		rt.sub = rest[1]

		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims /dns/v1/projects/{p}/managedZones[...] paths — a distinct URL
// space from the /v1/projects/ family (Firestore, IAM, Secret Manager, …), so
// registration order relative to them is unconstrained. Registered before the
// GCS fallback for consistency with the other GCP handlers.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	return ok && rt.project != ""
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud DNS path")
		return
	}

	switch {
	case rt.zone == "":
		h.serveZoneCollection(w, r, rt)
	case rt.sub == changesSeg:
		h.serveChanges(w, r, rt)
	case rt.sub == rrsetsSeg:
		h.serveRRSets(w, r, rt)
	case rt.sub == "":
		h.serveZone(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveZoneCollection dispatches /managedZones collection requests.
func (h *Handler) serveZoneCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createZone(w, r, rt)
	case http.MethodGet:
		h.listZones(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveZone dispatches /managedZones/{z} resource requests.
func (h *Handler) serveZone(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getZone(w, r, rt)
	case http.MethodDelete:
		h.deleteZone(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveChanges dispatches /managedZones/{z}/changes requests.
func (h *Handler) serveChanges(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		writeUnsupported(w)
		return
	}

	h.createChange(w, r, rt)
}

// serveRRSets dispatches /managedZones/{z}/rrsets requests.
func (h *Handler) serveRRSets(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodGet {
		writeUnsupported(w)
		return
	}

	h.listRRSets(w, r, rt)
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Cloud DNS operation")
}
