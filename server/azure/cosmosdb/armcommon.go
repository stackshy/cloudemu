package cosmosdb

// Shared ARM (Microsoft.DocumentDB) control-plane logic used by both the SQL-API
// handler (sqlarm.go) and the Mongo-API handler (mongoarm.go). A Cosmos database
// and its provisioned throughput are one shared backend entity regardless of the
// account's API kind — the account's databases set and the offers map back both
// planes — so the database plane, the throughput plane, and the request-routing
// and child-resource skeletons are written once here and parameterized per API
// by armAPISpec plus a few callbacks. Only the child resource itself (a SQL
// container vs a Mongo collection) has an API-specific shape (partition key +
// TTL/unique keys vs shard key + indexes), supplied by each handler's callbacks.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// armAPISpec names the API-specific path segments and ARM resource-type strings
// for one Cosmos API family, so the shared planes render the right "type" and
// build the right resource IDs while operating on the one shared backend.
type armAPISpec struct {
	databasesSegment       string // "sqlDatabases" | "mongodbDatabases"
	childSegment           string // "containers"   | "collections"
	databaseType           string // .../{databasesSegment}
	databaseThroughputType string // .../{databasesSegment}/throughputSettings
	childThroughputType    string // .../{databasesSegment}/{childSegment}/throughputSettings
	// databaseColls/databaseUsers are the SQL database's _colls/_users child
	// links, set on every SQL database response; the Mongo database resource has
	// neither, so they are left empty (omitted) for the Mongo plane.
	databaseColls string
	databaseUsers string
}

// Request routing (shared) ---------------------------------------------------

// childOps supplies the two API-specific child-resource entry points (a SQL
// container or a Mongo collection) to the shared dispatcher.
type childOps struct {
	list   func(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string)
	single func(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, child string)
}

// serveARMSubtree parses an ARM databases-subtree request, verifies the parent
// account exists, then hands the parsed path to dispatch. Both control planes
// route through this; only the databases/child segment names and the dispatch
// closure differ.
func serveARMSubtree(
	h *Handler, w http.ResponseWriter, r *http.Request, dbSeg, childSeg, notFound string,
	dispatch func(rp *azurearm.ResourcePath, t *sqlTarget),
) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	target, ok := parseDBSubtree(armTail(r.URL.Path, rp.ResourceName), dbSeg, childSeg)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", notFound)
		return
	}

	// Every operation is scoped to a database account; a missing account is
	// ARM's ParentResourceNotFound.
	if !h.isAccount(rp.ResourceName) {
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound",
			"database account "+rp.ResourceName+" not found")

		return
	}

	dispatch(&rp, &target)
}

// armDispatch routes a parsed target: database and throughput kinds go to the
// shared plane, child kinds go to the API-specific callbacks in ops.
func armDispatch(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	t *sqlTarget, spec *armAPISpec, notFound string, ops childOps,
) {
	switch t.kind {
	case kindDBList:
		armListDatabases(h, w, r, rp, spec)
	case kindDB:
		armDatabaseResource(h, w, r, rp, t.db, spec)
	case kindContainerList:
		ops.list(w, r, rp, t.db)
	case kindContainer:
		ops.single(w, r, rp, t.db, t.container)
	case kindDBThroughput, kindContainerThroughput:
		armServeThroughput(h, w, r, rp, t, spec)
	case kindDBMigrate, kindContainerMigrate:
		armMigrateThroughput(h, w, r, rp, t, spec)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", notFound)
	}
}

// Database plane (shared) -----------------------------------------------------

// armDatabaseResource serves GET/PUT/DELETE on a single database. Delete is
// idempotent (a cascade over a missing database still answers 204 so the SDK's
// BeginDelete poller terminates).
func armDatabaseResource(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string, spec *armAPISpec,
) {
	switch r.Method {
	case http.MethodPut:
		armCreateOrUpdateDatabase(h, w, r, rp, db, spec)
	case http.MethodGet:
		if !h.databaseExists(rp.ResourceName, db) {
			azurearm.WriteError(w, http.StatusNotFound, "NotFound", "database "+db+" not found")
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, armRenderDatabase(h, rp, db, spec))
	case http.MethodDelete:
		_ = h.cascadeDeleteDatabase(r.Context(), rp.ResourceName, db)

		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// armCreateOrUpdateDatabase is the create-or-update contract: a re-PUT of an
// existing database is not an error, it re-applies database-level throughput.
func armCreateOrUpdateDatabase(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db string, spec *armAPISpec,
) {
	var body armDatabaseCreateParams
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if body.Properties == nil || body.Properties.Resource == nil || body.Properties.Resource.ID == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "properties.resource.id is required")
		return
	}

	h.registerDatabase(rp.ResourceName, db)

	// Shared (database-level) throughput, keyed by the database's dbNS — the same
	// key the data plane's offer lookup derives, so it round-trips there too.
	if st, ok := offerFromOptions(body.Properties.Options); ok {
		h.setOffer(dbNS(rp.ResourceName, db), st)
	}

	azurearm.WriteJSON(w, http.StatusOK, armRenderDatabase(h, rp, db, spec))
}

// armListDatabases lists every database in the account (both APIs share the one
// databases set; a single-API account only ever holds one API's databases).
func armListDatabases(h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, spec *armAPISpec) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	names := databasesInAccount(h, rp.ResourceName)

	out := armDatabaseList{Value: make([]armDatabaseGetResults, 0, len(names))}
	for _, name := range names {
		out.Value = append(out.Value, armRenderDatabase(h, rp, name, spec))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// databasesInAccount returns the sorted database names owned by account.
func databasesInAccount(h *Handler, account string) []string {
	h.dbMu.RLock()
	defer h.dbMu.RUnlock()

	names := make([]string, 0)

	for key := range h.databases {
		if id, ok := accountDBName(key, account); ok {
			names = append(names, id)
		}
	}

	sort.Strings(names)

	return names
}

func armRenderDatabase(h *Handler, rp *azurearm.ResourcePath, db string, spec *armAPISpec) armDatabaseGetResults {
	id := armDBResourceID(rp, db, spec)

	return armDatabaseGetResults{
		ID:   id,
		Name: db,
		Type: spec.databaseType,
		Properties: &armDatabaseGetProps{
			Resource: &armDatabaseGetResource{
				ID:    db,
				RID:   "rid-" + dbNS(rp.ResourceName, db),
				TS:    h.clock.Now().Unix(),
				ETag:  azurearm.ETag(id),
				Colls: spec.databaseColls,
				Users: spec.databaseUsers,
			},
		},
	}
}

// Child resource skeletons (shared) -------------------------------------------

// singleChildOps supplies the API-specific behavior for a single child resource
// (SQL container / Mongo collection): PUT create-or-update, GET render, and the
// extra out-of-driver bookkeeping to drop on DELETE.
type singleChildOps struct {
	put     func(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, child string)
	render  func(ctx context.Context, rp *azurearm.ResourcePath, db, child string) (any, error)
	cleanup func(table string)
}

// armServeChild serves GET/PUT/DELETE on one child resource. DELETE is
// idempotent (absence is a no-op that still answers 204 so BeginDelete
// terminates) and drops the child's table, offer, and API-specific attrs.
func armServeChild(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, child string, ops singleChildOps,
) {
	switch r.Method {
	case http.MethodPut:
		ops.put(w, r, rp, db, child)
	case http.MethodGet:
		res, err := ops.render(r.Context(), rp, db, child)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, res)
	case http.MethodDelete:
		table := qualify(rp.ResourceName, db, child)
		_ = h.db.DeleteTable(r.Context(), table)
		h.deleteOffer(table)
		ops.cleanup(table)
		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// armListChildren lists the children (containers/collections) of a database in
// the shared {"value":[...]} envelope, rendering each via the API-specific
// render callback. A missing database is ARM's ParentResourceNotFound.
func armListChildren(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, db, notFound string,
	render func(ctx context.Context, rp *azurearm.ResourcePath, db, child string) (any, error),
) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	if !h.databaseExists(rp.ResourceName, db) {
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound", notFound)
		return
	}

	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	prefix := dbNS(rp.ResourceName, db) + "/"

	value := make([]any, 0)

	for _, t := range tables {
		if !strings.HasPrefix(t, prefix) {
			continue
		}

		if res, cerr := render(r.Context(), rp, db, strings.TrimPrefix(t, prefix)); cerr == nil {
			value = append(value, res)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

// Throughput plane (shared) ---------------------------------------------------

func armServeThroughput(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, t *sqlTarget, spec *armAPISpec,
) {
	key, resType, exists := armThroughputTarget(h, rp, t, spec)
	if !exists {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "parent resource not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		st, ok := h.getOffer(key)
		if !ok {
			azurearm.WriteError(w, http.StatusNotFound, "NotFound",
				"resource does not have dedicated throughput (shared or serverless)")

			return
		}

		azurearm.WriteJSON(w, http.StatusOK, renderThroughput(st, armThroughputID(rp, t, spec), resType))
	case http.MethodPut:
		armUpdateThroughput(h, w, r, rp, t, key, resType, spec)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func armUpdateThroughput(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	t *sqlTarget, key, resType string, spec *armAPISpec,
) {
	var body armThroughputUpdateParams
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var res *armThroughputResource
	if body.Properties != nil {
		res = body.Properties.Resource
	}

	if res == nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "properties.resource is required")
		return
	}

	st, ok := offerFromThroughput(res.Throughput, res.AutoscaleSettings)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter",
			"either throughput or autoscaleSettings.maxThroughput is required")

		return
	}

	h.setOffer(key, st)
	azurearm.WriteJSON(w, http.StatusOK, renderThroughput(st, armThroughputID(rp, t, spec), resType))
}

func armMigrateThroughput(
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, t *sqlTarget, spec *armAPISpec,
) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	key, resType, exists := armThroughputTarget(h, rp, t, spec)
	if !exists {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "parent resource not found")
		return
	}

	st, ok := h.getOffer(key)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"resource does not have dedicated throughput to migrate")

		return
	}

	migrated := migrateOffer(st, t.migrate == actionMigrateToAutoscale)
	h.setOffer(key, migrated)

	azurearm.WriteJSON(w, http.StatusOK, renderThroughput(migrated, armThroughputID(rp, t, spec), resType))
}

// armThroughputTarget resolves a throughput request to its offer key and
// response resource type, and reports whether the parent database/child exists.
func armThroughputTarget(
	h *Handler, rp *azurearm.ResourcePath, t *sqlTarget, spec *armAPISpec,
) (key, resType string, exists bool) {
	if t.container != "" {
		table := qualify(rp.ResourceName, t.db, t.container)
		if _, err := h.db.DescribeTable(context.Background(), table); err != nil {
			return "", "", false
		}

		return table, spec.childThroughputType, true
	}

	if !h.databaseExists(rp.ResourceName, t.db) {
		return "", "", false
	}

	return dbNS(rp.ResourceName, t.db), spec.databaseThroughputType, true
}

// Resource IDs ----------------------------------------------------------------

func armDBResourceID(rp *azurearm.ResourcePath, db string, spec *armAPISpec) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, armProvider, armAccountType, rp.ResourceName) +
		"/" + spec.databasesSegment + "/" + db
}

func armChildResourceID(rp *azurearm.ResourcePath, db, child string, spec *armAPISpec) string {
	return armDBResourceID(rp, db, spec) + "/" + spec.childSegment + "/" + child
}

// armThroughputID is the ARM ID of a throughputSettings/default child.
func armThroughputID(rp *azurearm.ResourcePath, t *sqlTarget, spec *armAPISpec) string {
	base := armDBResourceID(rp, t.db, spec)
	if t.container != "" {
		base += "/" + spec.childSegment + "/" + t.container
	}

	return base + "/throughputSettings/" + throughputName
}
