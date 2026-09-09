// Package bigquery implements the bigquery.googleapis.com v2 REST metadata
// control plane (datasets + tables) as a server.Handler. Real
// google.golang.org/api/bigquery/v2 clients, gcloud, and the Terraform
// google_bigquery_dataset / google_bigquery_table resources — pointed at this
// server with an endpoint that keeps the /bigquery/v2/ path prefix — CRUD and
// list datasets and tables, with full schema round-trip, end to end against the
// shared bigquery driver.
//
// Coverage (v2 REST, all synchronous — BigQuery metadata has no LRO):
//
//	POST   /bigquery/v2/projects/{p}/datasets                       — Insert dataset
//	GET    /bigquery/v2/projects/{p}/datasets/{d}                   — Get dataset
//	GET    /bigquery/v2/projects/{p}/datasets                       — List datasets
//	PATCH  /bigquery/v2/projects/{p}/datasets/{d}                   — Patch dataset (merge)
//	PUT    /bigquery/v2/projects/{p}/datasets/{d}                   — Update dataset (replace)
//	DELETE /bigquery/v2/projects/{p}/datasets/{d}?deleteContents=   — Delete dataset
//	POST   /bigquery/v2/projects/{p}/datasets/{d}/tables           — Insert table
//	GET    /bigquery/v2/projects/{p}/datasets/{d}/tables/{t}       — Get table
//	GET    /bigquery/v2/projects/{p}/datasets/{d}/tables           — List tables
//	PATCH  /bigquery/v2/projects/{p}/datasets/{d}/tables/{t}       — Patch table (merge)
//	PUT    /bigquery/v2/projects/{p}/datasets/{d}/tables/{t}       — Update table (replace)
//	DELETE /bigquery/v2/projects/{p}/datasets/{d}/tables/{t}       — Delete table
//
// Query job execution, streaming inserts, ML models, routines, and data
// transfer are out of scope; a view's SQL round-trips as metadata but is never
// executed.
package bigquery

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// pathPrefix is the URL prefix that identifies a BigQuery v2 REST request. It
// is disjoint from the /v1/projects/ family, /compute/v1/, and /dns/v1/, so
// registration order relative to every other GCP handler is unconstrained.
const pathPrefix = "/bigquery/v2/projects/"

const (
	segDatasets = "datasets"
	segTables   = "tables"
)

// Path-segment counts after "/bigquery/v2/": [projects, {p}, datasets, ...].
const (
	minParts       = 3 // projects/{p}/datasets
	partsDataset   = 4 // projects/{p}/datasets/{d}
	partsTablesCol = 5 // projects/{p}/datasets/{d}/tables
	partsTable     = 6 // projects/{p}/datasets/{d}/tables/{t}
)

// Handler serves bigquery.googleapis.com v2 dataset + table requests.
type Handler struct {
	bq driver.BigQuery
}

// New returns a BigQuery handler backed by bq.
func New(bq driver.BigQuery) *Handler {
	return &Handler{bq: bq}
}

// route is a parsed BigQuery v2 path.
type route struct {
	project string
	dataset string // empty for the datasets collection
	tables  bool   // true when the path reaches .../tables
	table   string // empty for the tables collection
}

// parseRoute extracts the components of a BigQuery v2 path, or ok=false.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/bigquery/v2/"), "/")
	if len(parts) < minParts || parts[0] != "projects" || parts[2] != segDatasets {
		return route{}, false
	}

	rt := route{project: parts[1]}

	if len(parts) == minParts {
		return rt, true
	}

	rt.dataset = parts[3]

	if len(parts) == partsDataset {
		return rt, true
	}

	return parseTablesTail(parts, rt)
}

// parseTablesTail parses the .../tables[/{t}] tail of a dataset path.
func parseTablesTail(parts []string, rt route) (route, bool) {
	if parts[4] != segTables {
		return route{}, false
	}

	rt.tables = true

	switch len(parts) {
	case partsTablesCol:
		return rt, true
	case partsTable:
		rt.table = parts[5]
		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims /bigquery/v2/projects/{p}/datasets[...] paths.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	return ok && rt.project != ""
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized BigQuery path")
		return
	}

	switch {
	case rt.tables && rt.table == "":
		h.serveTableCollection(w, r, rt)
	case rt.tables:
		h.serveTable(w, r, rt)
	case rt.dataset == "":
		h.serveDatasetCollection(w, r, rt)
	default:
		h.serveDataset(w, r, rt)
	}
}

// serveDatasetCollection dispatches /datasets collection requests.
func (h *Handler) serveDatasetCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.insertDataset(w, r, rt)
	case http.MethodGet:
		h.listDatasets(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveDataset dispatches /datasets/{d} resource requests.
func (h *Handler) serveDataset(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getDataset(w, r, rt)
	case http.MethodPatch:
		h.patchDataset(w, r, rt, false)
	case http.MethodPut:
		h.patchDataset(w, r, rt, true)
	case http.MethodDelete:
		h.deleteDataset(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveTableCollection dispatches /tables collection requests.
func (h *Handler) serveTableCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.insertTable(w, r, rt)
	case http.MethodGet:
		h.listTables(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveTable dispatches /tables/{t} resource requests.
func (h *Handler) serveTable(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getTable(w, r, rt)
	case http.MethodPatch:
		h.patchTable(w, r, rt, false)
	case http.MethodPut:
		h.patchTable(w, r, rt, true)
	case http.MethodDelete:
		h.deleteTable(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "unsupported BigQuery operation")
}

// reqHost derives the scheme+host base for selfLink construction from the
// incoming request.
func reqHost(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

// datasetSelfLink builds a dataset's selfLink.
func datasetSelfLink(host, project, datasetID string) string {
	return host + "/bigquery/v2/projects/" + project + "/datasets/" + datasetID
}

// tableSelfLink builds a table's selfLink.
func tableSelfLink(host, project, datasetID, tableID string) string {
	return datasetSelfLink(host, project, datasetID) + "/tables/" + tableID
}
