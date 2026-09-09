// Package securesourcemanager implements the Google Secure Source Manager
// control plane (securesourcemanager.googleapis.com/v1) as a server.Handler.
// Real Secure Source Manager clients, gcloud, and the Terraform google
// provider's google_secure_source_manager_instance and
// google_secure_source_manager_repository resources (both GA in the stable
// `google` provider, served at the default /v1/ path) hit this handler
// unchanged.
//
// Coverage (instance + repository control plane):
//
//	POST   /v1/…/instances?instanceId=       — CreateInstance (LRO)
//	GET    /v1/…/instances                    — ListInstances
//	GET    /v1/…/instances/{id}               — GetInstance
//	PATCH  /v1/…/instances/{id}?updateMask=   — PatchInstance (LRO)
//	DELETE /v1/…/instances/{id}               — DeleteInstance (LRO)
//	POST   /v1/…/repositories?repositoryId=   — CreateRepository (LRO)
//	…                                          — Get/List/Patch/Delete (as above)
//	GET    /v1/…/operations/{op}              — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response` as an Any typed
// type.googleapis.com/google.cloud.securesourcemanager.v1.{Instance,Repository},
// so an SDK or Terraform LRO wait terminates on the first poll instead of
// hanging.
//
// Location-scoped operations: Secure Source Manager's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls.
//
// # Path collisions on /instances AND /repositories
//
// BOTH collections share their path grammar with other GCP services on the same
// /v1/ base path. CloudEmu collapses every GCP service onto one HTTP server, and
// a client pointed at it via a custom endpoint sends the emulator's own host,
// not the API host, so the services CANNOT be told apart by URL or Host alone.
// They are disambiguated by content and ownership, the pattern Filestore already
// uses to coexist with Memorystore — the greedy fall-through service claims
// everything not claimed by a selective handler registered ahead of it:
//
//   - /instances collides with Filestore (file.googleapis.com) and Memorystore
//     for Redis (redis.googleapis.com), the greedy fall-through. A create is
//     claimed only when the body carries NO Filestore signal (fileShares/
//     networks) and NO Memorystore signal (memorySizeGb/redisConfigs/
//     redisVersion/tier); an item/LIST only when this store owns the instance /
//     owns an instance in scope.
//   - /repositories collides with Artifact Registry
//     (artifactregistry.googleapis.com), the greedy fall-through. A create is
//     claimed only when the body carries the required `instance` reference (an
//     Artifact Registry repository carries a `format` instead, never `instance`);
//     an item/LIST only when this store owns the repository / owns one in scope.
//     (Dataform also serves a repositories collection but on the /v1beta1/ base
//     path, so it does not collide with this /v1/ handler.)
//
// A project+location holding two colliding services' resources of one kind is
// the single documented ambiguity (first-registered wins); every other request
// is unambiguous.
package securesourcemanager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/securesourcemanager/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	instancesColl    = "instances"
	repositoriesColl = "repositories"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// maxProbeBytes caps the request body read to sniff an instance-create's
	// shape (Redis vs Filestore vs Secure Source Manager) before the body is
	// restored for the real decode or a fall-through to a sibling handler.
	maxProbeBytes = 1 << 20

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, createTime, updateTime).
	minComputedFields = 3

	instanceTypeURL   = "type.googleapis.com/google.cloud.securesourcemanager.v1.Instance"
	repositoryTypeURL = "type.googleapis.com/google.cloud.securesourcemanager.v1.Repository"
)

// collection describes one Secure Source Manager resource collection, binding
// its wire identity to the driver methods that back it. Both collections share
// every CRUD code path, differing only in these bound values and a small set of
// computed-field hooks.
type collection struct {
	seg     string // "instances" | "repositories"
	idParam string // "instanceId" | "repositoryId"
	typeURL string

	// validate rejects a create body that violates a collection invariant (a
	// repository's required `instance` reference). Nil where none applies.
	validate func(map[string]json.RawMessage) error
	// seed injects computed body values minted once at create so a later GET
	// reports them stably (an instance's state + hostConfig, a repository's uid +
	// uris), derived deterministically from the resource identity.
	seed func(fields map[string]json.RawMessage, project, location, id string)

	create func(context.Context, *ssmdriver.Config) (*ssmdriver.Resource, *ssmdriver.Operation, error)
	get    func(context.Context, string, string, string) (*ssmdriver.Resource, error)
	list   func(context.Context, string, string) ([]ssmdriver.Resource, error)
	patch  func(context.Context, *ssmdriver.Config, []string) (*ssmdriver.Resource, *ssmdriver.Operation, error)
	del    func(context.Context, string, string, string) (*ssmdriver.Operation, error)
}

// Handler serves securesourcemanager.googleapis.com v1 requests against a
// SecureSourceManager driver.
type Handler struct {
	db ssmdriver.SecureSourceManager

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Secure Source Manager handler backed by db.
func New(db ssmdriver.SecureSourceManager) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the two resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		instancesColl: {
			seg: instancesColl, idParam: "instanceId", typeURL: instanceTypeURL,
			seed:   seedInstance,
			create: h.db.CreateInstance, get: h.db.GetInstance, list: h.db.ListInstances,
			del: h.db.DeleteInstance, // instances have no update RPC (patch stays nil)
		},
		repositoriesColl: {
			seg: repositoriesColl, idParam: "repositoryId", typeURL: repositoryTypeURL,
			validate: validateRepository, seed: seedRepository,
			create: h.db.CreateRepository, get: h.db.GetRepository, list: h.db.ListRepositories,
			patch: h.db.PatchRepository, del: h.db.DeleteRepository,
		},
	}
}

// route holds the parsed components of a Secure Source Manager v1 path.
type route struct {
	project  string
	location string
	resource string // "instances" | "repositories" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Secure Source Manager v1 path. It
// recognizes only the instances, repositories, and operations resources under a
// locations scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rest := parts[minResourceParts:]
	if len(rest) == 0 || len(rest) > itemParts || !knownResource(rest[0]) {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3], resource: rest[0]}
	if len(rest) == itemParts {
		rt.name = rest[1]
	}

	return rt, true
}

// knownResource reports whether seg is a resource collection this handler
// serves.
func knownResource(seg string) bool {
	return seg == instancesColl || seg == repositoriesColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{instances|repositories|
// operations}[/…] paths. Operations are claimed only when this handler has no
// shared LRO registry (a standalone package server); in an assembled server the
// shared poller owns them. Both instances (shared with Filestore/Memorystore)
// and repositories (shared with Artifact Registry) are claimed selectively by
// content (create) and ownership (item/list) — see the package doc.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	switch rt.resource {
	case operationsSeg:
		return h.ops == nil
	case repositoriesColl:
		return h.matchesRepository(r, rt)
	default: // instancesColl
		return h.matchesInstance(r, rt)
	}
}

// matchesRepository claims only genuinely-Secure-Source-Manager traffic on the
// /repositories path it shares with Artifact Registry (the greedy fall-through
// registered after it): an item request only when this store owns the
// repository, a bare LIST only when this store owns a repository in the
// addressed scope, and a create only when the body carries the required
// `instance` reference (an Artifact Registry repository has none).
func (h *Handler) matchesRepository(r *http.Request, rt route) bool {
	if rt.name != "" {
		return h.ownsRepository(r.Context(), rt.project, rt.location, rt.name)
	}

	switch r.Method {
	case http.MethodPost:
		return bodyHasInstanceRef(r)
	case http.MethodGet:
		return h.ownsAnyRepositoryIn(r.Context(), rt.project, rt.location)
	default:
		return false
	}
}

// ownsRepository reports whether this store holds the named repository.
func (h *Handler) ownsRepository(ctx context.Context, project, location, id string) bool {
	_, err := h.db.GetRepository(ctx, project, location, id)

	return err == nil
}

// ownsAnyRepositoryIn reports whether this store holds any repository in scope.
func (h *Handler) ownsAnyRepositoryIn(ctx context.Context, project, location string) bool {
	list, err := h.db.ListRepositories(ctx, project, location)

	return err == nil && len(list) > 0
}

// bodyHasInstanceRef reports whether a POST /repositories body carries a
// non-empty `instance` reference — the field every Secure Source Manager
// repository create must set and that an Artifact Registry repository create
// (which carries a `format` instead) never does. The body is read and restored
// so a fall-through to Artifact Registry still sees the full request.
func bodyHasInstanceRef(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxProbeBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		Instance string `json:"instance"`
	}

	if json.Unmarshal(raw, &probe) != nil {
		return false
	}

	return probe.Instance != ""
}

// matchesInstance claims only genuinely-Secure-Source-Manager traffic on the
// /instances path it shares with Filestore and Memorystore: an item request only
// when this store owns the instance, a bare LIST only when this store owns an
// instance in the addressed scope, and a create only when the body carries no
// Filestore or Memorystore signal.
func (h *Handler) matchesInstance(r *http.Request, rt route) bool {
	if rt.name != "" {
		return h.ownsInstance(r.Context(), rt.project, rt.location, rt.name)
	}

	switch r.Method {
	case http.MethodPost:
		return bodyLooksLikeSSMInstance(r)
	case http.MethodGet:
		return h.ownsAnyInstanceIn(r.Context(), rt.project, rt.location)
	default:
		return false
	}
}

// ownsInstance reports whether this store holds the named instance.
func (h *Handler) ownsInstance(ctx context.Context, project, location, id string) bool {
	_, err := h.db.GetInstance(ctx, project, location, id)

	return err == nil
}

// ownsAnyInstanceIn reports whether this store holds any instance in the scope.
func (h *Handler) ownsAnyInstanceIn(ctx context.Context, project, location string) bool {
	list, err := h.db.ListInstances(ctx, project, location)

	return err == nil && len(list) > 0
}

// bodyLooksLikeSSMInstance reports whether a POST /instances body is a Secure
// Source Manager Instance rather than a Filestore Instance (fileShares/networks)
// or a Memorystore Redis Instance (memorySizeGb/redisConfigs/redisVersion/tier).
// A Secure Source Manager instance carries none of those signals, so a body
// lacking every one of them is claimed. The body is read and restored so a
// fall-through to a sibling handler still sees the full request.
func bodyLooksLikeSSMInstance(r *http.Request) bool {
	if r.Body == nil {
		return true
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxProbeBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		FileShares   []any          `json:"fileShares"`
		Networks     []any          `json:"networks"`
		MemorySizeGb *float64       `json:"memorySizeGb"`
		RedisConfigs map[string]any `json:"redisConfigs"`
		RedisVersion string         `json:"redisVersion"`
		Tier         string         `json:"tier"`
	}

	if len(raw) > 0 && json.Unmarshal(raw, &probe) != nil {
		return false
	}

	filestoreSignal := len(probe.FileShares) > 0 || len(probe.Networks) > 0
	redisSignal := probe.MemorySizeGb != nil || len(probe.RedisConfigs) > 0 ||
		probe.RedisVersion != "" || probe.Tier != ""

	return !filestoreSignal && !redisSignal
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Secure Source Manager path")
		return
	}

	if rt.resource == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	col := h.collections()[rt.resource]
	if col == nil {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, rt, col)
		return
	}

	h.serveItem(w, r, rt, col)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	switch r.Method {
	case http.MethodPost:
		h.createResource(w, r, rt, col)
	case http.MethodGet:
		h.listResources(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	switch r.Method {
	case http.MethodGet:
		h.getResource(w, r, rt, col)
	case http.MethodPatch:
		if col.patch == nil { // instances have no update RPC in the real API
			writeMethodNotAllowed(w)
			return
		}

		h.patchResource(w, r, rt, col)
	case http.MethodDelete:
		h.deleteResource(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
