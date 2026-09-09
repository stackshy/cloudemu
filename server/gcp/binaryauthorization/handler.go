// Package binaryauthorization implements the binaryauthorization.googleapis.com
// v1 REST API as a server.Handler. Real
// google.golang.org/api/binaryauthorization/v1 clients — and Terraform's google
// provider (google_binary_authorization_policy / google_binary_authorization_
// attestor) — pointed at this server manage the per-project Policy singleton and
// CRUD attestors end-to-end against the Binary Authorization driver.
//
// Coverage (v1 REST), all SYNCHRONOUS (the resource, or Empty for delete, or the
// IAM Policy, is returned directly — Binary Authorization has no long-running
// operations):
//
//	GET    /v1/projects/{p}/policy                              — Get policy (singleton)
//	PUT    /v1/projects/{p}/policy                              — Update policy (full replace)
//	POST   /v1/projects/{p}/attestors?attestorId={id}          — Create attestor
//	GET    /v1/projects/{p}/attestors/{a}                      — Get attestor
//	GET    /v1/projects/{p}/attestors                          — List attestors (paged)
//	PUT    /v1/projects/{p}/attestors/{a}                      — Update attestor (full replace)
//	DELETE /v1/projects/{p}/attestors/{a}                      — Delete attestor
//	POST   /v1/projects/{p}/attestors/{a}:getIamPolicy         — Get IAM policy
//	POST   /v1/projects/{p}/attestors/{a}:setIamPolicy         — Set IAM policy
//	POST   /v1/projects/{p}/attestors/{a}:testIamPermissions   — Test IAM permissions
//
// This is the control plane only. The policy singleton has no create/delete; a
// project that never set a policy reads back a seeded default. Deep config blocks
// round-trip verbatim.
package binaryauthorization

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	badriver "github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"
)

const (
	pathPrefix = "/v1/projects/"

	policySeg    = "policy"
	attestorsSeg = "attestors"

	// resourceParts counts [projects, {p}, policy|attestors].
	resourceParts = 3
	// attestorNameParts counts [projects, {p}, attestors, {a}].
	attestorNameParts = 4

	verbGetIamPolicy       = "getIamPolicy"
	verbSetIamPolicy       = "setIamPolicy"
	verbTestIamPermissions = "testIamPermissions"

	defaultPageSize = 500
	maxPageSize     = 1000
)

// Handler serves binaryauthorization.googleapis.com v1 requests.
type Handler struct {
	ba badriver.BinaryAuthorization
}

// New returns a Binary Authorization handler backed by ba.
func New(ba badriver.BinaryAuthorization) *Handler {
	return &Handler{ba: ba}
}

// route holds the parsed components of a Binary Authorization v1 path.
type route struct {
	project    string
	policy     bool   // /policy singleton
	collection bool   // /attestors collection (no id)
	attestor   string // attestor id, for a resource path
	verb       string // *IamPolicy | testIamPermissions, if any
}

// attestorName returns the projects/{p}/attestors/{a} resource name.
func (rt route) attestorName() string {
	return "projects/" + rt.project + "/attestors/" + rt.attestor
}

// parseRoute extracts the components of a Binary Authorization v1 path. It claims
// only the /policy singleton and the /attestors collection+resource paths, so it
// is disjoint from every sibling GCP project-scoped grammar (operations, etc.).
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < resourceParts || parts[0] != "projects" || parts[1] == "" {
		return route{}, false
	}

	return classifyResource(route{project: parts[1]}, parts)
}

// classifyResource fills rt from the resource-type segment (parts[2]), claiming
// only the /policy singleton and the /attestors collection+resource paths.
func classifyResource(rt route, parts []string) (route, bool) {
	switch {
	case parts[2] == policySeg && len(parts) == resourceParts:
		rt.policy = true
		return rt, true
	case parts[2] == attestorsSeg && len(parts) == resourceParts:
		rt.collection = true
		return rt, true
	case parts[2] == attestorsSeg && len(parts) == attestorNameParts:
		rt.attestor, rt.verb, _ = strings.Cut(parts[3], ":")
		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims only /v1/projects/{p}/policy and /v1/projects/{p}/attestors[/…]
// paths. It never claims operations or any sibling grammar, so registration
// order is unconstrained relative to other resource-type-guarded handlers (but
// it must register before Firestore's permissive /v1/projects/ prefix).
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Binary Authorization path")
		return
	}

	switch {
	case rt.policy:
		h.servePolicy(w, r, rt)
	case rt.collection:
		h.serveCollection(w, r, rt)
	case rt.verb != "":
		h.serveVerb(w, r, rt)
	default:
		h.serveAttestor(w, r, rt)
	}
}

// servePolicy dispatches the /policy singleton (GET + PUT only).
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getPolicy(w, r, rt)
	case http.MethodPut:
		h.updatePolicy(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveCollection dispatches /attestors collection requests.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createAttestor(w, r, rt)
	case http.MethodGet:
		h.listAttestors(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveAttestor dispatches /attestors/{id} resource requests.
func (h *Handler) serveAttestor(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getAttestor(w, r, rt)
	case http.MethodPut:
		h.updateAttestor(w, r, rt)
	case http.MethodDelete:
		h.deleteAttestor(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveVerb dispatches the colon custom IAM methods. getIamPolicy is a GET (it
// carries only query options); setIamPolicy and testIamPermissions are POSTs
// with a request body.
func (h *Handler) serveVerb(w http.ResponseWriter, r *http.Request, rt route) {
	switch {
	case rt.verb == verbGetIamPolicy && r.Method == http.MethodGet:
		h.getIamPolicy(w, r, rt)
	case rt.verb == verbSetIamPolicy && r.Method == http.MethodPost:
		h.setIamPolicy(w, r, rt)
	case rt.verb == verbTestIamPermissions && r.Method == http.MethodPost:
		h.testIamPermissions(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	p, err := h.ba.GetPolicy(r.Context(), rt.project)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyJSON(p))
}

func (h *Handler) updatePolicy(w http.ResponseWriter, r *http.Request, rt route) {
	var body policyJSON
	if !decodeBody(w, r, &body) {
		return
	}

	cfg := body.toPolicyConfig()

	p, err := h.ba.UpdatePolicy(r.Context(), rt.project, &cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyJSON(p))
}

func (h *Handler) createAttestor(w http.ResponseWriter, r *http.Request, rt route) {
	var body attestorJSON
	if !decodeBody(w, r, &body) {
		return
	}

	// attestorId is a query param; fall back to the trailing segment of the name
	// in the body if a client sent it that way.
	id := r.URL.Query().Get("attestorId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "attestorId is required")
		return
	}

	name := "projects/" + rt.project + "/attestors/" + id

	a, err := h.ba.CreateAttestor(r.Context(), rt.project, id, body.toAttestorConfig(name))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toAttestorJSON(a))
}

func (h *Handler) getAttestor(w http.ResponseWriter, r *http.Request, rt route) {
	a, err := h.ba.GetAttestor(r.Context(), rt.attestorName())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toAttestorJSON(a))
}

func (h *Handler) listAttestors(w http.ResponseWriter, r *http.Request, rt route) {
	attestors, err := h.ba.ListAttestors(r.Context(), rt.project)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(attestors,
		func(a, b badriver.Attestor) bool { return a.Name < b.Name },
		pageToken(r), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]attestorJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toAttestorJSON(&page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listAttestorsResponse{Attestors: out, NextPageToken: page.NextPageToken})
}

func (h *Handler) updateAttestor(w http.ResponseWriter, r *http.Request, rt route) {
	var body attestorJSON
	if !decodeBody(w, r, &body) {
		return
	}

	a, err := h.ba.UpdateAttestor(r.Context(), body.toAttestorConfig(rt.attestorName()))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toAttestorJSON(a))
}

func (h *Handler) deleteAttestor(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.ba.DeleteAttestor(r.Context(), rt.attestorName()); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// google.protobuf.Empty.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Binary Authorization operation")
}

// decodeBody reads a request body, normalizing integer enums to their canonical
// names first. An empty body decodes to the zero value. Returns false (and writes
// a 400) on a decode error.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, gcprest.MaxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "read body: "+err.Error())
		return false
	}

	if len(body) == 0 {
		return true
	}

	if err := json.Unmarshal(normalizeEnumNumbers(body), v); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

// lastSegment returns the trailing path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// pageSize reads ?pageSize, clamping to a sane default and ceiling.
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	if n > maxPageSize {
		return maxPageSize
	}

	return n
}

// pageToken reads the opaque ?pageToken continuation cursor.
func pageToken(r *http.Request) string {
	return r.URL.Query().Get("pageToken")
}
