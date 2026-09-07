package dataplex

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 1000
)

// levels binds the three resource levels to h's driver method values, keyed by
// their levelKind. The get/list/del closures pull the parent ids from the parsed
// route; create/patch receive a fully-built Config.
func (h *Handler) levels() map[levelKind]*level {
	return map[levelKind]*level{
		levelLake: {
			seg: lakesSeg, idParam: "lakeId", typeURL: lakeTypeURL,
			injectComputed: injectLakeComputed,
			create:         h.db.CreateLake,
			get: func(ctx context.Context, rt *route) (*dpdriver.Resource, error) {
				return h.db.GetLake(ctx, rt.project, rt.location, rt.name)
			},
			list: func(ctx context.Context, rt *route) ([]dpdriver.Resource, error) {
				return h.db.ListLakes(ctx, rt.project, rt.location)
			},
			patch: h.db.PatchLake,
			del: func(ctx context.Context, rt *route) (*dpdriver.Operation, error) {
				return h.db.DeleteLake(ctx, rt.project, rt.location, rt.name)
			},
		},
		levelZone: {
			seg: zonesSeg, idParam: "zoneId", typeURL: zoneTypeURL,
			validate:       validateZone,
			injectComputed: injectZoneComputed,
			create:         h.db.CreateZone,
			get: func(ctx context.Context, rt *route) (*dpdriver.Resource, error) {
				return h.db.GetZone(ctx, rt.project, rt.location, rt.lake, rt.name)
			},
			list: func(ctx context.Context, rt *route) ([]dpdriver.Resource, error) {
				return h.db.ListZones(ctx, rt.project, rt.location, rt.lake)
			},
			patch: h.db.PatchZone,
			del: func(ctx context.Context, rt *route) (*dpdriver.Operation, error) {
				return h.db.DeleteZone(ctx, rt.project, rt.location, rt.lake, rt.name)
			},
		},
		levelAsset: {
			seg: assetsSeg, idParam: "assetId", typeURL: assetTypeURL,
			validate:       validateAsset,
			injectComputed: injectAssetComputed,
			create:         h.db.CreateAsset,
			get: func(ctx context.Context, rt *route) (*dpdriver.Resource, error) {
				return h.db.GetAsset(ctx, rt.project, rt.location, rt.lake, rt.zone, rt.name)
			},
			list: func(ctx context.Context, rt *route) ([]dpdriver.Resource, error) {
				return h.db.ListAssets(ctx, rt.project, rt.location, rt.lake, rt.zone)
			},
			patch: h.db.PatchAsset,
			del: func(ctx context.Context, rt *route) (*dpdriver.Operation, error) {
				return h.db.DeleteAsset(ctx, rt.project, rt.location, rt.lake, rt.zone, rt.name)
			},
		},
	}
}

// config builds the driver Config for a create/patch: the parent ids come from
// the route, id from the caller.
func (rt *route) config(id string, fields map[string]json.RawMessage) *dpdriver.Config {
	return &dpdriver.Config{
		Project: rt.project, Location: rt.location, Lake: rt.lake, Zone: rt.zone, ID: id, Fields: fields,
	}
}

// createResource handles POST .../{collection}?{idParam}=. The id is the query
// param, falling back to the trailing segment of the body name. A level's
// validate hook rejects a malformed body (a zone type/location_type enum, an
// asset resource_spec.type enum). The operation completes inline.
func (h *Handler) createResource(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := r.URL.Query().Get(lvl.idParam)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", lvl.idParam+" is required")
		return
	}

	if lvl.validate != nil {
		if err := lvl.validate(fields); err != nil {
			gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", err.Error())
			return
		}
	}

	res, op, err := lvl.create(r.Context(), rt.config(id, fields))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, rt, lvl, op, res)
}

// getResource handles GET .../{collection}/{id}.
func (*Handler) getResource(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	res, err := lvl.get(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeResource(w, rt, lvl, res)
}

// listResources handles GET .../{collection}, scoped to the request's parent and
// ordered by resource id.
func (*Handler) listResources(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	all, err := lvl.list(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b dpdriver.Resource) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := renderResource(rt, lvl, &page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		lvl.seg:         items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchResource handles PATCH .../{collection}/{id}?updateMask=. Only the masked
// top-level fields mutate. A level's validate hook still guards enum fields the
// mask touches. The operation completes inline.
func (h *Handler) patchResource(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	if lvl.validate != nil {
		if err := lvl.validate(fields); err != nil {
			gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", err.Error())
			return
		}
	}

	res, op, err := lvl.patch(r.Context(), rt.config(rt.name, fields), parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, rt, lvl, op, res)
}

// deleteResource handles DELETE .../{collection}/{id}. The operation completes
// inline with no response.
func (h *Handler) deleteResource(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	op, err := lvl.del(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// serveOperation resolves a (done) long-running operation poll for a standalone
// package server (no shared registry). The operation resource name is the request
// path without the /v1/ version prefix.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/v1/")

	op, err := h.db.GetOperation(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: op.Name, Done: true})
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
