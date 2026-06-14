package azuresearch

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	srchdriver "github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/errors"
)

const maxDPBody = 8 << 20

// dataPlaneRoots are the path prefixes the data-plane handler claims.
//
//nolint:gochecknoglobals // immutable routing set
var dataPlaneRoots = map[string]bool{
	"indexes": true, "indexers": true, "datasources": true,
	"skillsets": true, "synonymmaps": true, "aliases": true, "servicestats": true,
}

// DataPlaneHandler serves the {service}.search.windows.net data plane. The
// service scope is derived from the request Host subdomain.
type DataPlaneHandler struct {
	dp srchdriver.SearchDataPlane
}

// NewDataPlane returns a data-plane handler backed by drv.
func NewDataPlane(drv srchdriver.SearchDataPlane) *DataPlaneHandler {
	return &DataPlaneHandler{dp: drv}
}

// Matches claims the search data-plane roots.
func (*DataPlaneHandler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)

	return len(parts) > 0 && dataPlaneRoots[parts[0]]
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}

// serviceFromHost extracts the service name from {service}.search.windows.net,
// falling back to "default" for hosts without that suffix (e.g. httptest).
func serviceFromHost(host string) string {
	host, _, _ = strings.Cut(host, ":")
	if i := strings.Index(host, ".search."); i > 0 {
		return host[:i]
	}

	return "default"
}

// ServeHTTP routes by the top-level data-plane resource.
func (h *DataPlaneHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	service := serviceFromHost(r.Host)

	switch parts[0] {
	case "indexes":
		h.serveIndexes(w, r, service, parts)
	case "indexers":
		h.serveIndexers(w, r, service, parts)
	case "datasources":
		h.serveDataSources(w, r, service, parts)
	case "skillsets":
		h.serveSkillsets(w, r, service, parts)
	case "synonymmaps":
		h.serveSynonymMaps(w, r, service, parts)
	case "aliases":
		h.serveAliases(w, r, service, parts)
	case "servicestats":
		h.serveServiceStats(w, r, service)
	default:
		dpErr(w, http.StatusNotFound, "unknown data-plane resource: "+parts[0])
	}
}

// --- Indexes + documents ---

func indexJSON(i *srchdriver.Index) map[string]any {
	fields := make([]map[string]any, 0, len(i.Fields))
	for _, f := range i.Fields {
		fields = append(fields, map[string]any{
			"name": f.Name, "type": f.Type, "key": f.Key,
			"searchable": f.Searchable, "filterable": f.Filterable,
			"sortable": f.Sortable, "facetable": f.Facetable,
			"retrievable": f.Retrievable, "dimensions": f.Dimensions,
		})
	}

	return map[string]any{"name": i.Name, "fields": fields, "@odata.etag": i.ETag}
}

func (h *DataPlaneHandler) serveIndexes(w http.ResponseWriter, r *http.Request, service string, parts []string) {
	// /indexes/{name}/docs/...
	const docsIdx = 2
	if len(parts) > docsIdx && parts[docsIdx] == "docs" {
		h.serveDocs(w, r, service, parts[1], parts)

		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPost:
			h.putIndex(w, r, service, "")
		case http.MethodGet:
			h.listIndexes(w, r, service)
		default:
			dpMethodNotAllowed(w)
		}

		return
	}

	name := parts[1]

	switch r.Method {
	case http.MethodPut:
		h.putIndex(w, r, service, name)
	case http.MethodGet:
		idx, err := h.dp.GetIndex(r.Context(), service, name)
		writeDP(w, indexJSON, idx, err)
	case http.MethodDelete:
		dpDelete(w, h.dp.DeleteIndex(r.Context(), service, name))
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) putIndex(w http.ResponseWriter, r *http.Request, service, name string) {
	var body struct {
		Name   string `json:"name"`
		Fields []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Key         bool   `json:"key"`
			Searchable  bool   `json:"searchable"`
			Filterable  bool   `json:"filterable"`
			Sortable    bool   `json:"sortable"`
			Facetable   bool   `json:"facetable"`
			Retrievable bool   `json:"retrievable"`
			Dimensions  int    `json:"dimensions"`
		} `json:"fields"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	if body.Name == "" {
		body.Name = name
	}

	idx := srchdriver.Index{Name: body.Name}
	for _, f := range body.Fields {
		idx.Fields = append(idx.Fields, srchdriver.Field{
			Name: f.Name, Type: f.Type, Key: f.Key, Searchable: f.Searchable,
			Filterable: f.Filterable, Sortable: f.Sortable, Facetable: f.Facetable,
			Retrievable: f.Retrievable, Dimensions: f.Dimensions,
		})
	}

	out, err := h.dp.CreateOrUpdateIndex(r.Context(), service, idx)
	writeDP(w, indexJSON, out, err)
}

func (h *DataPlaneHandler) listIndexes(w http.ResponseWriter, r *http.Request, service string) {
	idxs, err := h.dp.ListIndexes(r.Context(), service)
	if err != nil {
		dpCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(idxs))
	for i := range idxs {
		out = append(out, indexJSON(&idxs[i]))
	}

	dpJSON(w, map[string]any{"value": out})
}

// --- wire helpers ---

func dpDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDPBody))
	if err != nil {
		dpErr(w, http.StatusBadRequest, "failed to read body")

		return false
	}

	if strings.TrimSpace(string(body)) == "" {
		return true
	}

	if err := json.Unmarshal(body, v); err != nil {
		dpErr(w, http.StatusBadRequest, "invalid JSON body")

		return false
	}

	return true
}

func dpJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func dpErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "message": msg}})
}

func dpMethodNotAllowed(w http.ResponseWriter) {
	dpErr(w, http.StatusMethodNotAllowed, "method not allowed")
}

func dpCErr(w http.ResponseWriter, err error) {
	switch {
	case errors.IsNotFound(err):
		dpErr(w, http.StatusNotFound, err.Error())
	case errors.IsInvalidArgument(err):
		dpErr(w, http.StatusBadRequest, err.Error())
	case errors.IsAlreadyExists(err), errors.IsFailedPrecondition(err):
		dpErr(w, http.StatusConflict, err.Error())
	default:
		dpErr(w, http.StatusInternalServerError, err.Error())
	}
}

func dpDelete(w http.ResponseWriter, err error) {
	if err != nil {
		dpCErr(w, err)

		return
	}

	dpJSON(w, map[string]any{})
}

// writeDP writes a created/fetched data-plane resource or maps the error.
func writeDP[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, res *T, err error) {
	if err != nil {
		dpCErr(w, err)

		return
	}

	dpJSON(w, toJSON(res))
}
