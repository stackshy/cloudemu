// Package alloydb implements the GCP AlloyDB Admin REST API as a
// server.Handler. Real google.golang.org/api/alloydb/v1 clients configured
// with a custom endpoint hit this handler the same way they hit
// alloydb.googleapis.com.
//
// Coverage (all mutating endpoints return Operation envelopes with done=true so
// SDK callers observe a terminal operation immediately):
//
//	POST   /v1/projects/{p}/locations/{l}/clusters?clusterId={c}            — CreateCluster
//	POST   /v1/projects/{p}/locations/{l}/clusters:createsecondary?clusterId={c} — CreateSecondary
//	GET    /v1/projects/{p}/locations/{l}/clusters                          — ListClusters
//	GET    /v1/projects/{p}/locations/{l}/clusters/{c}                      — GetCluster
//	PATCH  /v1/projects/{p}/locations/{l}/clusters/{c}                      — UpdateCluster
//	DELETE /v1/projects/{p}/locations/{l}/clusters/{c}                      — DeleteCluster
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}:promote              — PromoteCluster
//	POST   /v1/projects/{p}/locations/{l}/clusters/{c}/instances?instanceId={i} — CreateInstance
//	GET    …/clusters/{c}/instances[/{i}]                                   — List/Get instance
//	PATCH  …/clusters/{c}/instances/{i}                                     — UpdateInstance
//	DELETE …/clusters/{c}/instances/{i}                                     — DeleteInstance
//	POST   …/clusters/{c}/instances/{i}:failover|:restart                   — instance actions
//	POST   …/clusters/{c}/users?userId={u}, GET/DELETE …/users[/{u}]        — users
//	POST   /v1/projects/{p}/locations/{l}/backups?backupId={b}              — CreateBackup
//	GET/DELETE /v1/projects/{p}/locations/{l}/backups[/{b}]                 — Get/List/Delete backup
//	GET    /v1/projects/{p}/locations/{l}/operations/{op}                   — poll (always done)
//
// The /v1/projects/ prefix is shared; Matches narrows to
// .../locations/{l}/{clusters|backups|operations} so Vertex AI, Cloud SQL,
// Pub/Sub, etc. fall through.
package alloydb

import (
	"net/http"
	"strings"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	pathPrefix      = "/v1/projects/"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20

	collectionClusters   = "clusters"
	collectionBackups    = "backups"
	collectionOperations = "operations"

	subInstances = "instances"
	subUsers     = "users"

	locationsSeg = "locations"

	actionCreateSecondary = "createsecondary"
	actionRestore         = "restore"
	actionPromote         = "promote"
	actionFailover        = "failover"
	actionRestart         = "restart"
)

// Handler serves AlloyDB Admin REST requests against a relationaldb driver that
// also implements the AlloyDB optional capability.
type Handler struct {
	db rdsdriver.RelationalDB
}

// New returns an AlloyDB handler backed by db.
func New(db rdsdriver.RelationalDB) *Handler {
	return &Handler{db: db}
}

// Matches claims /v1/projects/{p}/locations/{l}/{clusters|backups|operations}
// paths. Everything else under /v1/projects/ falls through to other GCP
// handlers.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")

	const idxScope, idxCollection = 1, 3
	if len(parts) <= idxCollection || parts[idxScope] != locationsSeg {
		return false
	}

	switch collectionOf(parts[idxCollection]) {
	case collectionClusters, collectionBackups, collectionOperations:
		return true
	default:
		return false
	}
}

// alloyPath is the parsed AlloyDB request path.
type alloyPath struct {
	project    string
	location   string
	collection string // clusters | backups | operations
	collAction string // createsecondary | restore (custom method on the collection)

	clusterID     string
	clusterAction string // promote (custom method on a cluster)

	sub       string // instances | users
	subID     string
	subAction string // failover | restart (custom method on an instance)

	backupID    string
	operationID string
}

// collectionOf strips any ":method" suffix from a collection segment.
func collectionOf(seg string) string {
	if i := strings.IndexByte(seg, ':'); i >= 0 {
		return seg[:i]
	}

	return seg
}

// splitAction splits a "{id}:action" segment into its id and action.
func splitAction(seg string) (id, action string) {
	if i := strings.IndexByte(seg, ':'); i >= 0 {
		return seg[:i], seg[i+1:]
	}

	return seg, ""
}

// parsePath parses a matched AlloyDB URL. ok=false for a malformed path.
func parsePath(urlPath string) (alloyPath, bool) {
	parts := strings.Split(strings.TrimPrefix(urlPath, pathPrefix), "/")

	const minParts = 4 // {p}/locations/{l}/{collection}
	if len(parts) < minParts || parts[1] != locationsSeg {
		return alloyPath{}, false
	}

	p := alloyPath{project: parts[0], location: parts[2]}
	p.collection, p.collAction = splitAction(parts[3])

	rest := parts[4:]

	switch p.collection {
	case collectionOperations:
		if len(rest) > 0 {
			p.operationID = rest[0]
		}
	case collectionBackups:
		if len(rest) > 0 {
			p.backupID = rest[0]
		}
	case collectionClusters:
		parseClusterTail(&p, rest)
	}

	return p, true
}

// parseClusterTail fills the cluster/instance/user portion of the path.
func parseClusterTail(p *alloyPath, rest []string) {
	if len(rest) == 0 {
		return
	}

	p.clusterID, p.clusterAction = splitAction(rest[0])

	if len(rest) >= 2 { //nolint:mnd // {c}/{sub}
		p.sub = rest[1]
	}

	if len(rest) >= 3 { //nolint:mnd // {c}/{sub}/{id}
		p.subID, p.subAction = splitAction(rest[2])
	}
}

// ServeHTTP routes a matched AlloyDB request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := parsePath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unrecognized AlloyDB path")
		return
	}

	switch p.collection {
	case collectionOperations:
		h.serveOperation(w, r, &p)
	case collectionBackups:
		h.serveBackups(w, r, &p)
	case collectionClusters:
		h.serveClusters(w, r, &p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported AlloyDB collection")
	}
}

// serveClusters dispatches cluster-, instance- and user-scoped requests.
func (h *Handler) serveClusters(w http.ResponseWriter, r *http.Request, p *alloyPath) {
	switch {
	case p.sub == subInstances:
		h.serveInstances(w, r, p)
	case p.sub == subUsers:
		h.serveUsers(w, r, p)
	case p.clusterID == "" && p.collAction == actionCreateSecondary:
		h.createSecondaryCluster(w, r, p)
	case p.clusterID == "" && p.collAction == actionRestore:
		h.restoreCluster(w, r, p)
	case p.clusterID == "":
		h.serveClusterCollection(w, r, p)
	default:
		h.serveClusterItem(w, r, p)
	}
}
