// Package kusto serves Azure Data Explorer (Kusto) ARM control-plane requests
// (Microsoft.Kusto/clusters and their databases).
//
// Real azure-sdk-for-go armkusto clients drive this surface. Clusters own their
// databases; deleting a cluster cascades to every database it holds. Clusters
// are created Running with a synthesized query URI and data-ingestion URI, and
// Start/Stop flip the cluster state. Databases default to the ReadWrite kind.
//
// SCOPE: this Handler is the ARM CONTROL-PLANE only (clusters + databases CRUD,
// list, start/stop). The Kusto QUERY data plane — the /v1|v2/rest/{mgmt,query}
// endpoints clients POST to <cluster>.<region>.kusto.windows.net — is served by
// the separate DataPlaneHandler (dataplane.go) registered alongside it. That
// handler currently serves the control commands (.create/.show/.drop table)
// against an in-memory table store; the KQL query evaluator lands in a later
// increment.
package kusto

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName        = "Microsoft.Kusto"
	segClusters         = "clusters"
	segDatabases        = "databases"
	segStart            = "start"
	segStop             = "stop"
	clusterResourceType = "Microsoft.Kusto/Clusters"
	dbResourceType      = "Microsoft.Kusto/Clusters/Databases"

	maxBodyBytes = 1 << 20

	// namePairLen is the length of a {keyword}/{value} pair such as clusters/{c}.
	namePairLen = 2

	// listPageSize is how many entities a list returns before emitting a nextLink.
	listPageSize = 100
	// skipParam is the query parameter a paged list request carries to resume at
	// an offset, mirroring the $skip nextLink real Azure returns.
	skipParam = "$skip"
)

// Handler serves ARM Kusto (Azure Data Explorer) requests.
type Handler struct {
	mu       sync.RWMutex
	clusters *memstore.Store[*clusterState]
}

// New returns a Kusto control-plane handler.
func New() *Handler {
	return &Handler{clusters: memstore.New[*clusterState]()}
}

// kustoPath is a parsed Kusto ARM URL. segs holds the path segments that follow
// the cluster name (e.g. ["start"] or ["databases","mydb"]).
type kustoPath struct {
	sub     string
	rg      string
	cluster string
	segs    []string
}

// parseKustoPath parses a Kusto ARM path. ok is false for non-Kusto paths and
// for malformed provider paths.
func parseKustoPath(urlPath string) (kustoPath, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")

	providerIdx, ok := findProvider(parts)
	if !ok {
		return kustoPath{}, false
	}

	kp := parseScope(parts, providerIdx)

	rest := parts[providerIdx+namePairLen:]
	if len(rest) == 0 || !strings.EqualFold(rest[0], segClusters) {
		return kustoPath{}, false
	}

	if len(rest) >= namePairLen {
		kp.cluster = rest[1]
		kp.segs = rest[namePairLen:]
	}

	return kp, true
}

// findProvider returns the index of the "providers" segment when it is
// immediately followed by the Kusto provider name.
func findProvider(parts []string) (int, bool) {
	for i, s := range parts {
		if !strings.EqualFold(s, "providers") {
			continue
		}

		if i+1 < len(parts) && strings.EqualFold(parts[i+1], providerName) {
			return i, true
		}

		return 0, false
	}

	return 0, false
}

// parseScope reads the subscription and resource group from the segments that
// precede the providers segment.
func parseScope(parts []string, providerIdx int) kustoPath {
	kp := kustoPath{}

	for i := 0; i+1 < providerIdx; i++ {
		switch {
		case strings.EqualFold(parts[i], "subscriptions"):
			kp.sub = parts[i+1]
		case strings.EqualFold(parts[i], "resourceGroups"):
			kp.rg = parts[i+1]
		}
	}

	return kp
}

// Matches accepts Kusto ARM paths.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseKustoPath(r.URL.Path)

	return ok
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	kp, ok := parseKustoPath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch {
	case kp.cluster == "":
		h.listClusters(w, r, kp)
	case len(kp.segs) == 0:
		h.serveCluster(w, r, kp)
	default:
		h.serveClusterChild(w, r, kp)
	}
}

// serveClusterChild dispatches the routes under a cluster.
func (h *Handler) serveClusterChild(w http.ResponseWriter, r *http.Request, kp kustoPath) {
	switch {
	case eq(kp.segs[0], segStart):
		h.setClusterState(w, kp, stateRunning)
	case eq(kp.segs[0], segStop):
		h.setClusterState(w, kp, stateStopped)
	case eq(kp.segs[0], segDatabases) && len(kp.segs) == 1:
		h.listDatabases(w, r, kp)
	case eq(kp.segs[0], segDatabases) && len(kp.segs) == namePairLen:
		h.serveDatabase(w, r, kp, kp.segs[1])
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "unsupported path")
	}
}

func eq(a, b string) bool { return strings.EqualFold(a, b) }

// clusterKey normalizes a cluster name to its store key; Kusto cluster names map
// 1:1 to a DNS host, so real Azure treats them case-insensitively.
func clusterKey(name string) string { return strings.ToLower(name) }

// lookupCluster returns the cluster state if it exists and matches the request
// scope. Callers hold h.mu.
func (h *Handler) lookupCluster(kp kustoPath) (*clusterState, bool) {
	c, ok := h.clusters.Get(clusterKey(kp.cluster))
	if !ok {
		return nil, false
	}

	if !strings.EqualFold(c.Subscription, kp.sub) || !strings.EqualFold(c.ResourceGroup, kp.rg) {
		return nil, false
	}

	return c, true
}

func writeClusterNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "cluster not found: "+name)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// paginate returns the listPageSize-sized window of resources that starts at the
// request's $skip offset, emitting a nextLink when more items remain.
func paginate(r *http.Request, resources []any) listResponse {
	skip := paginationSkip(r)
	if skip >= len(resources) {
		return listResponse{Value: []any{}}
	}

	end := skip + listPageSize
	if end >= len(resources) {
		return listResponse{Value: resources[skip:]}
	}

	return listResponse{Value: resources[skip:end], NextLink: nextPageLink(r, end)}
}

func paginationSkip(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get(skipParam))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// nextPageLink builds the absolute URL that continues a listing at offset skip,
// preserving the request path and query and overriding $skip.
func nextPageLink(r *http.Request, skip int) string {
	next := *r.URL
	next.Host = r.Host

	next.Scheme = "http"
	if r.TLS != nil {
		next.Scheme = "https"
	}

	q := next.Query()
	q.Set(skipParam, strconv.Itoa(skip))
	next.RawQuery = q.Encode()

	return next.String()
}
