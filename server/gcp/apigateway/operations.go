package apigateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	agdriver "github.com/stackshy/cloudemu/v2/services/apigatewaygcp/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// collection describes one API Gateway resource collection, binding its wire
// identity to the driver methods that back it. The three collections share every
// CRUD code path, differing only in these bound values and a computed-field seed
// hook. The get/list/del closures take the parsed route so a config's parent api
// flows through uniformly with the flat apis/gateways collections.
type collection struct {
	seg       string // "apis" | "configs" | "gateways"
	protoKind string // "Api" | "ApiConfig" | "Gateway"
	idParam   string // "apiId" | "apiConfigId" | "gatewayId"

	// seed injects computed body values minted once at create (state, an
	// apiConfig's serviceConfigId, a gateway's defaultHostname) so a later GET
	// reports them stably.
	seed func(fields map[string]json.RawMessage, rt *route)

	create func(context.Context, *agdriver.Config) (*agdriver.Resource, *agdriver.Operation, error)
	get    func(context.Context, *route) (*agdriver.Resource, error)
	list   func(context.Context, *route) ([]agdriver.Resource, error)
	patch  func(context.Context, *agdriver.Config, []string) (*agdriver.Resource, *agdriver.Operation, error)
	del    func(context.Context, *route) (*agdriver.Operation, error)
}

// flatVerbs bundles the driver methods of a flat, project+location-scoped
// collection (apis, gateways) whose get/list/delete key only on project,
// location, and id.
type flatVerbs struct {
	create func(context.Context, *agdriver.Config) (*agdriver.Resource, *agdriver.Operation, error)
	get    func(context.Context, string, string, string) (*agdriver.Resource, error)
	list   func(context.Context, string, string) ([]agdriver.Resource, error)
	patch  func(context.Context, *agdriver.Config, []string) (*agdriver.Resource, *agdriver.Operation, error)
	del    func(context.Context, string, string, string) (*agdriver.Operation, error)
}

// flatCollection wraps a flat collection's driver methods into the route-taking
// closures the CRUD handlers call, shared by the apis and gateways collections
// (a config nests under an api and is built separately).
func flatCollection(seg, protoKind, idParam string, seed func(map[string]json.RawMessage, *route), v flatVerbs) *collection {
	return &collection{
		seg: seg, protoKind: protoKind, idParam: idParam, seed: seed,
		create: v.create,
		get: func(ctx context.Context, rt *route) (*agdriver.Resource, error) {
			return v.get(ctx, rt.project, rt.location, rt.name)
		},
		list: func(ctx context.Context, rt *route) ([]agdriver.Resource, error) {
			return v.list(ctx, rt.project, rt.location)
		},
		patch: v.patch,
		del: func(ctx context.Context, rt *route) (*agdriver.Operation, error) {
			return v.del(ctx, rt.project, rt.location, rt.name)
		},
	}
}

// collections binds each resource collection to h's driver method values, keyed
// by the parsed path level.
func (h *Handler) collections() map[levelKind]*collection {
	return map[levelKind]*collection{
		levelAPI: flatCollection(apisSeg, "Api", "apiId", seedAPI, flatVerbs{
			create: h.db.CreateAPI, get: h.db.GetAPI, list: h.db.ListAPIs,
			patch: h.db.PatchAPI, del: h.db.DeleteAPI,
		}),
		levelGateway: flatCollection(gatewaysSeg, "Gateway", "gatewayId", seedGateway, flatVerbs{
			create: h.db.CreateGateway, get: h.db.GetGateway, list: h.db.ListGateways,
			patch: h.db.PatchGateway, del: h.db.DeleteGateway,
		}),
		levelConfig: {
			seg: configsSeg, protoKind: "ApiConfig", idParam: "apiConfigId", seed: seedConfig,
			create: h.db.CreateAPIConfig,
			get: func(ctx context.Context, rt *route) (*agdriver.Resource, error) {
				return h.db.GetAPIConfig(ctx, rt.project, rt.location, rt.api, rt.name)
			},
			list: func(ctx context.Context, rt *route) ([]agdriver.Resource, error) {
				return h.db.ListAPIConfigs(ctx, rt.project, rt.location, rt.api)
			},
			patch: h.db.PatchAPIConfig,
			del: func(ctx context.Context, rt *route) (*agdriver.Operation, error) {
				return h.db.DeleteAPIConfig(ctx, rt.project, rt.location, rt.api, rt.name)
			},
		},
	}
}

// createResource handles POST .../{collection}?{idParam}=. The id is the query
// param, falling back to the trailing segment of the body name. A collection's
// seed hook mints computed values (state, serviceConfigId, defaultHostname) so a
// later GET is stable. The operation completes inline.
func (h *Handler) createResource(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := r.URL.Query().Get(col.idParam)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", col.idParam+" is required")
		return
	}

	rt.name = id
	if col.seed != nil {
		col.seed(fields, rt)
	}

	res, op, err := col.create(r.Context(), &agdriver.Config{
		Project: rt.project, Location: rt.location, API: rt.api, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, col, rt.version, op, res)
}

// getResource handles GET .../{collection}/{id}.
func (*Handler) getResource(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
	res, err := col.get(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeResource(w, col, res)
}

// listResources handles GET .../{collection}, scoped to the request's parent and
// ordered by resource name.
func (*Handler) listResources(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
	all, err := col.list(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b agdriver.Resource) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := col.toResourceJSON(&page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		col.seg:         items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchResource handles PATCH .../{collection}/{id}?updateMask=. Only the masked
// top-level fields mutate. The operation completes inline.
func (h *Handler) patchResource(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	res, op, err := col.patch(r.Context(), &agdriver.Config{
		Project: rt.project, Location: rt.location, API: rt.api, ID: rt.name, Fields: fields,
	}, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, col, rt.version, op, res)
}

// deleteResource handles DELETE .../{collection}/{id}. The operation completes
// inline with no response.
func (h *Handler) deleteResource(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
	op, err := col.del(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// serveOperation resolves a (done) long-running operation poll. The google-beta
// provider polls at its /v1beta/ base path — a space the shared LRO poller does
// not own — so this handler always answers it; a standalone /v1/ package server
// (no shared registry) is answered here too. The operation resource name is the
// request path without the version prefix.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/"+rt.version+"/")

	op, err := h.db.GetOperation(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: op.Name, Done: true})
}

// writeResource renders a driver resource as apigateway wire JSON.
func writeResource(w http.ResponseWriter, col *collection, res *agdriver.Resource) {
	raw, err := col.toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// writeResourceOperation writes a completed operation carrying the resource as
// its Any-typed response (create/patch).
func (h *Handler) writeResourceOperation(
	w http.ResponseWriter, col *collection, version string, op *agdriver.Operation, res *agdriver.Resource,
) {
	raw, err := col.toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, col.responseAny(version, raw)))
}

// doneOperation builds a completed google.longrunning.Operation and records it
// with the shared LRO poller (a no-op on a nil registry) so a /v1/ client polling
// the returned name resolves the same done operation (with its response).
func (h *Handler) doneOperation(name string, resp json.RawMessage) operationJSON {
	if h.ops != nil {
		h.ops.Register(name, resp)
	}

	return operationJSON{Name: name, Done: true, Response: resp}
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
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
