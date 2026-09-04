package kusto

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Kusto query data-plane wire paths. Real clients POST to the cluster host
// (<cluster>.<region>.kusto.windows.net); these paths are globally unique and
// claimed by no other handler, so the data-plane handler slots into the
// dispatcher in any registration order.
const (
	pathMgmt    = "/v1/rest/mgmt"
	pathQueryV1 = "/v1/rest/query"
	pathQueryV2 = "/v2/rest/query"

	// defaultCluster is the cluster namespace used when the request Host is not a
	// Kusto cluster DNS name — the httptest / bare-IP case, and cloudemu serve on
	// its HTTPS port, where every request lands on the same server regardless of
	// host. This keeps the SDK/CLI pointed at a plain endpoint working.
	defaultCluster = "cloudemu"

	// kustoDNSSuffix is the DNS suffix a Kusto cluster query host carries; the
	// leftmost label is the cluster name.
	kustoDNSSuffix = ".kusto.windows.net"
	// ingestLabelPrefix is stripped from the cluster label of an ingestion host.
	ingestLabelPrefix = "ingest-"

	dataMaxBodyBytes = 1 << 20
)

// DataPlaneHandler serves the Kusto query data plane: control commands
// (/v1/rest/mgmt) and — once the KQL engine lands — queries (/v1|v2/rest/query).
// It owns the ingested table state, scoped per (cluster, database), and is
// driverless like the ARM control-plane Handler.
type DataPlaneHandler struct {
	data *dataStore
}

// NewDataPlane returns a Kusto data-plane handler with an empty table store.
func NewDataPlane() *DataPlaneHandler {
	return &DataPlaneHandler{data: newDataStore()}
}

// Matches accepts the Kusto data-plane wire paths. Query is allowed over GET and
// POST (as the SDK permits); mgmt is POST-only. Matching is case-insensitive and
// tolerant of a trailing slash.
func (*DataPlaneHandler) Matches(r *http.Request) bool {
	switch dataPath(r.URL.Path) {
	case pathMgmt:
		return r.Method == http.MethodPost
	case pathQueryV1, pathQueryV2:
		return r.Method == http.MethodPost || r.Method == http.MethodGet
	default:
		return false
	}
}

// dataPath canonicalises a request path to one of the known data-plane paths, or
// "" when it is none of them.
func dataPath(p string) string {
	p = strings.ToLower(strings.TrimRight(p, "/"))
	switch p {
	case pathMgmt, pathQueryV1, pathQueryV2:
		return p
	default:
		return ""
	}
}

// dataRequest is the request body shared by all three endpoints. Properties
// (Options / Parameters) is accepted but ignored in this increment.
type dataRequest struct {
	DB         string          `json:"db"`
	CSL        string          `json:"csl"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// ServeHTTP decodes the request body and routes by canonical path.
func (h *DataPlaneHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDataRequest(w, r)
	if !ok {
		return
	}

	cluster := clusterFromHost(r.Host)

	switch dataPath(r.URL.Path) {
	case pathMgmt:
		h.serveMgmt(w, cluster, req)
	case pathQueryV1, pathQueryV2:
		serveQueryNotImplemented(w)
	default:
		writeDataError(w, http.StatusNotFound, "PathNotFound", "unknown Kusto data-plane path")
	}
}

func decodeDataRequest(w http.ResponseWriter, r *http.Request) (dataRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, dataMaxBodyBytes)

	var req dataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeDataError(w, http.StatusBadRequest, "BadRequest", "malformed request body: "+err.Error())
		return dataRequest{}, false
	}

	return req, true
}

// serveQueryNotImplemented answers the query endpoints while the KQL engine is
// not yet wired. The paths are registered so the query evaluator slots in later;
// until then a clean 501 is returned rather than a broken partial frame.
func serveQueryNotImplemented(w http.ResponseWriter) {
	writeDataError(w, http.StatusNotImplemented, "NotImplemented",
		"KQL query execution is not yet implemented; only control commands (/v1/rest/mgmt) are served")
}

// clusterFromHost resolves the cluster namespace from the request Host. A Kusto
// cluster host (<cluster>[.region].kusto.windows.net) yields its leftmost label
// with any ingest- prefix stripped; any other host (bare httptest IP, cloudemu
// serve port) falls back to the single default cluster.
func clusterFromHost(host string) string {
	if host == "" {
		return defaultCluster
	}

	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}

	host = strings.ToLower(host)
	if !strings.HasSuffix(host, kustoDNSSuffix) {
		return defaultCluster
	}

	label := host[:len(host)-len(kustoDNSSuffix)]
	if dot := strings.IndexByte(label, '.'); dot >= 0 {
		label = label[:dot]
	}

	label = strings.TrimPrefix(label, ingestLabelPrefix)
	if label == "" {
		return defaultCluster
	}

	return label
}
