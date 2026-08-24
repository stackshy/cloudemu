// Package cosmosdb implements the Azure Cosmos DB SQL data-plane REST API
// against a CloudEmu database driver. Real azure-sdk-for-go/sdk/data/azcosmos
// clients configured with a custom endpoint hit this handler the same way
// they hit {account}.documents.azure.com.
//
// Supported operations (parity with AWS DynamoDB):
//
//	Databases:  POST /dbs, GET /dbs, GET /dbs/{db}, DELETE /dbs/{db}
//	Containers: POST /dbs/{db}/colls, GET /dbs/{db}/colls, GET .../{c}, DELETE .../{c}
//	Items:      POST /dbs/{db}/colls/{c}/docs   (also Query with x-ms-documentdb-isquery)
//	            GET .../docs, GET .../docs/{id}
//	            PUT .../docs/{id}, DELETE .../docs/{id}
//
// The generic driver has a flat table namespace with no database-level
// grouping, so the handler models the database layer itself: it tracks which
// databases were created (listable and individually addressable via the {db}
// path segment) and qualifies every container's driver-table name by its
// database, giving each database an isolated container namespace. A container
// in one database is therefore unreachable through another, and deleting a
// database removes the containers it owns.
package cosmosdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/cosmossql"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
	// idAttr is the Cosmos document id attribute, which also serves as the
	// driver sort key so a document's identity is (partition key, id).
	idAttr = "id"
	// offersPath / offersPathPrefix address the /offers throughput resource.
	offersPath       = "/offers"
	offersPathPrefix = "/offers/"
)

// Handler serves Cosmos DB SQL API requests against a database driver.
type Handler struct {
	db dbdriver.Database

	// offers holds the provisioned throughput declared at container-create time,
	// keyed by the container's resource id ("_rid"). Throughput is a Cosmos-only
	// concept with no generic driver method, so the wire handler tracks it.
	offerMu sync.RWMutex
	offers  map[string]offerState

	// databases tracks the Cosmos databases that exist. The generic driver has a
	// flat table namespace with no database grouping, so the wire handler models
	// the database layer: it records which databases were created and qualifies
	// every container's driver-table name by its database (see qualify), giving
	// each database an isolated container namespace.
	dbMu      sync.RWMutex
	databases map[string]struct{}
}

// New returns a Cosmos handler backed by db.
func New(db dbdriver.Database) *Handler {
	return &Handler{
		db:        db,
		offers:    make(map[string]offerState),
		databases: make(map[string]struct{}),
	}
}

// qualify maps a (database, container) pair onto the flat driver table name that
// backs it. Cosmos container ids cannot contain '/', so "{db}/{coll}" is an
// unambiguous, collision-free key: a container in one database is unreachable
// through another, and deleting a database can find its containers by prefix.
func qualify(db, coll string) string {
	return db + "/" + coll
}

// databaseExists reports whether database db has been created.
func (h *Handler) databaseExists(db string) bool {
	h.dbMu.RLock()
	_, ok := h.databases[db]
	h.dbMu.RUnlock()

	return ok
}

// Matches returns true for the Cosmos data plane URLs we serve: the account
// root probe (GET /), the /dbs/... resource tree, and the /offers throughput
// resource.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return p == "/" || p == "/dbs" || strings.HasPrefix(p, "/dbs/") ||
		p == offersPath || strings.HasPrefix(p, offersPathPrefix)
}

// ServeHTTP routes the request based on URL path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		h.accountProperties(w, r)
		return
	}

	if r.URL.Path == offersPath || strings.HasPrefix(r.URL.Path, offersPathPrefix) {
		h.serveOffers(w, r)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	switch len(parts) {
	case 1:
		// /dbs
		h.databaseCollection(w, r)
	case pathDBOnly:
		// /dbs/{db}
		h.databaseResource(w, r, parts[1])
	case pathColls:
		// /dbs/{db}/colls
		h.containerCollection(w, r, parts[1])
	case pathContainerOrDocsCol:
		// /dbs/{db}/colls/{coll}
		h.containerResource(w, r, parts[1], parts[3])
	case pathDocsCol:
		// /dbs/{db}/colls/{coll}/docs
		h.documentCollection(w, r, parts[1], parts[3])
	case pathDocResource:
		// /dbs/{db}/colls/{coll}/docs/{id}
		h.documentResource(w, r, parts[1], parts[3], parts[5])
	default:
		writeError(w, http.StatusNotFound, "NotFound", "unsupported Cosmos path")
	}
}

// Path-segment counts. Defined as constants so it's easy to reason about
// nested resource depth without magic numbers.
const (
	pathDBOnly             = 2 // /dbs/{db}
	pathColls              = 3 // /dbs/{db}/colls
	pathContainerOrDocsCol = 4 // /dbs/{db}/colls/{coll}
	pathDocsCol            = 5 // /dbs/{db}/colls/{coll}/docs
	pathDocResource        = 6 // /dbs/{db}/colls/{coll}/docs/{id}
)

// accountProperties answers the account-root probe the Cosmos SDK fires on
// first use. The response shape mimics a global database-account record;
// real Cosmos returns regions, consistency settings, etc.
func (*Handler) accountProperties(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	endpoint := scheme + "://" + r.Host + "/"

	props := map[string]any{
		idAttr:                         "cloudemu",
		"_rid":                         "cloudemu",
		"_self":                        "",
		"_etag":                        `"cloudemu"`,
		"_ts":                          time.Now().Unix(),
		"_dbs":                         "//dbs/",
		"writableLocations":            []map[string]any{{"name": "Primary", "databaseAccountEndpoint": endpoint}},
		"readableLocations":            []map[string]any{{"name": "Primary", "databaseAccountEndpoint": endpoint}},
		"enableMultipleWriteLocations": false,
		"userConsistencyPolicy": map[string]any{
			"defaultConsistencyLevel": "Session",
		},
		"systemReplicationPolicy": map[string]any{
			"minReplicaSetSize":     1,
			"maxReplicasetSize":     4,
			"asyncReplication":      false,
			"replicaRestoreTimeout": 600,
		},
		"userReplicationPolicy": map[string]any{
			"minReplicaSetSize":     1,
			"maxReplicasetSize":     4,
			"asyncReplication":      false,
			"replicaRestoreTimeout": 600,
		},
		"addresses":             "//addresses/",
		"userResourceGroupName": "",
	}

	writeJSON(w, http.StatusOK, props)
}

func (h *Handler) databaseCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// Cosmos overloads POST /dbs: a create (JSON body with an id) or, when the
		// isquery flag is set, a query the SDK's database pager fires. Drain the
		// query body but ignore its predicate — we list all databases.
		if isQuery(r) {
			_, _ = decodeQueryBody(w, r)
			h.writeDatabaseList(w)

			return
		}

		h.createDatabase(w, r)
	case http.MethodGet:
		h.writeDatabaseList(w)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "BadRequest", "database id required")
		return
	}

	h.dbMu.Lock()
	if _, exists := h.databases[body.ID]; exists {
		h.dbMu.Unlock()
		writeError(w, http.StatusConflict, "Conflict",
			"Database with specified id already exists.")

		return
	}

	h.databases[body.ID] = struct{}{}
	h.dbMu.Unlock()

	writeJSON(w, http.StatusCreated, makeDatabaseResource(body.ID))
}

func (h *Handler) writeDatabaseList(w http.ResponseWriter) {
	h.dbMu.RLock()
	names := make([]string, 0, len(h.databases))

	for name := range h.databases {
		names = append(names, name)
	}

	h.dbMu.RUnlock()
	sort.Strings(names)

	list := databasesList{RID: "cloudemu", Databases: make([]databaseResource, 0, len(names))}
	for _, name := range names {
		list.Databases = append(list.Databases, makeDatabaseResource(name))
	}

	list.Count = len(list.Databases)
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) databaseResource(w http.ResponseWriter, r *http.Request, db string) {
	switch r.Method {
	case http.MethodGet:
		if !h.databaseExists(db) {
			writeError(w, http.StatusNotFound, "NotFound", "database not found")
			return
		}

		writeJSON(w, http.StatusOK, makeDatabaseResource(db))
	case http.MethodDelete:
		h.deleteDatabase(w, r, db)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// deleteDatabase removes a database and every container inside it. Because the
// driver namespace is flat and keyed by qualify(db, coll), the database's
// containers are exactly the tables whose name starts with "{db}/".
func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request, db string) {
	if !h.databaseExists(db) {
		writeError(w, http.StatusNotFound, "NotFound", "database not found")
		return
	}

	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	prefix := db + "/"

	for _, t := range tables {
		if !strings.HasPrefix(t, prefix) {
			continue
		}

		if derr := h.db.DeleteTable(r.Context(), t); derr != nil {
			writeErr(w, derr)
			return
		}

		h.deleteOffer(t)
	}

	h.dbMu.Lock()
	delete(h.databases, db)
	h.dbMu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) containerCollection(w http.ResponseWriter, r *http.Request, db string) {
	switch r.Method {
	case http.MethodPost:
		h.createContainer(w, r, db)
	case http.MethodGet:
		h.listContainers(w, r, db)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createContainer(w http.ResponseWriter, r *http.Request, db string) {
	if !h.databaseExists(db) {
		writeError(w, http.StatusNotFound, "NotFound", "database not found")
		return
	}

	var body containerResource

	if !decodeJSON(w, r, &body) {
		return
	}

	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "BadRequest", "container id required")
		return
	}

	pkAttr := partitionKeyAttribute(body.PartitionKey)

	cfg := dbdriver.TableConfig{
		Name:         qualify(db, body.ID),
		PartitionKey: pkAttr,
	}

	// Cosmos identifies a document by (partition-key value, id). The generic
	// driver keys an item by PartitionKey (+ SortKey), so we map the document id
	// onto the sort key to get that composite identity. When the partition key
	// path is /id the two coincide, so no sort key is needed.
	if pkAttr != idAttr {
		cfg.SortKey = idAttr
	}

	if err := h.db.CreateTable(r.Context(), cfg); err != nil {
		writeErr(w, err)
		return
	}

	h.recordOffer(qualify(db, body.ID), r)

	writeJSON(w, http.StatusCreated, makeContainerResource(db, body.ID, body.PartitionKey))
}

func (h *Handler) listContainers(w http.ResponseWriter, r *http.Request, db string) {
	if !h.databaseExists(db) {
		writeError(w, http.StatusNotFound, "NotFound", "database not found")
		return
	}

	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := containersList{RID: "cloudemu"}
	prefix := db + "/"

	for _, t := range tables {
		if !strings.HasPrefix(t, prefix) {
			continue
		}

		coll := strings.TrimPrefix(t, prefix)

		cfg, derr := h.db.DescribeTable(r.Context(), t)
		pk := defaultPartitionKey()

		if derr == nil && cfg != nil && cfg.PartitionKey != "" {
			pk = &partitionKeyDef{Paths: []string{"/" + cfg.PartitionKey}, Kind: "Hash"}
		}

		out.DocumentCollections = append(out.DocumentCollections,
			makeContainerResource(db, coll, pk))
	}

	out.Count = len(out.DocumentCollections)

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) containerResource(w http.ResponseWriter, r *http.Request, db, coll string) {
	table := qualify(db, coll)

	switch r.Method {
	case http.MethodGet:
		cfg, err := h.db.DescribeTable(r.Context(), table)
		if err != nil {
			writeErr(w, err)
			return
		}

		pk := defaultPartitionKey()
		if cfg.PartitionKey != "" {
			pk = &partitionKeyDef{Paths: []string{"/" + cfg.PartitionKey}, Kind: "Hash"}
		}

		writeJSON(w, http.StatusOK, makeContainerResource(db, coll, pk))
	case http.MethodDelete:
		if err := h.db.DeleteTable(r.Context(), table); err != nil {
			writeErr(w, err)
			return
		}

		h.deleteOffer(table)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) documentCollection(w http.ResponseWriter, r *http.Request, db, coll string) {
	table := qualify(db, coll)

	switch r.Method {
	case http.MethodPost:
		// Cosmos overloads POST /docs for both create and query depending on
		// the x-ms-documentdb-isquery header.
		if isQuery(r) {
			h.queryDocuments(w, r, table)
			return
		}

		h.createDocument(w, r, table)
	case http.MethodGet:
		h.listDocuments(w, r, table)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request, coll string) {
	item, ok := decodeAnyJSON(w, r)
	if !ok {
		return
	}

	if _, exists := item[idAttr]; !exists {
		writeError(w, http.StatusBadRequest, "BadRequest", "item must contain an id field")
		return
	}

	cfg, err := h.db.DescribeTable(r.Context(), coll)
	if err != nil {
		writeErr(w, err)
		return
	}

	// A plain create (not an upsert) must fail if a document with the same
	// (partition key, id) already exists, matching real Cosmos's 409 Conflict.
	if !isUpsert(r) && h.documentExists(r.Context(), coll, cfg, item) {
		writeError(w, http.StatusConflict, "Conflict",
			"Resource with specified id or name already exists.")
		return
	}

	if err := h.db.PutItem(r.Context(), coll, item); err != nil {
		writeErr(w, err)
		return
	}

	addSystemProps(item)
	writeJSON(w, http.StatusCreated, item)
}

// documentExists reports whether a document with item's (partition key, id)
// identity is already stored in coll.
func (h *Handler) documentExists(
	ctx context.Context, coll string, cfg *dbdriver.TableConfig, item map[string]any,
) bool {
	key := map[string]any{idAttr: item[idAttr]}
	if cfg != nil && cfg.PartitionKey != "" && cfg.PartitionKey != idAttr {
		key[cfg.PartitionKey] = item[cfg.PartitionKey]
	}

	_, err := h.db.GetItem(ctx, coll, key)

	return err == nil
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request, coll string) {
	// Same paging contract as the query path: x-ms-max-item-count is the
	// page size, x-ms-continuation round-trips the driver page token.
	limit := 100

	if v := r.Header.Get("X-Ms-Max-Item-Count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{
		Table:     coll,
		Limit:     limit,
		PageToken: r.Header.Get("X-Ms-Continuation"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	docs := make([]any, 0, len(result.Items))

	for i := range result.Items {
		addSystemProps(result.Items[i])
		docs = append(docs, result.Items[i])
	}

	if result.NextPageToken != "" {
		w.Header().Set("X-Ms-Continuation", result.NextPageToken)
	}

	writeJSON(w, http.StatusOK, documentsList{
		RID:       "cloudemu",
		Documents: docs,
		Count:     result.Count,
	})
}

func (h *Handler) queryDocuments(w http.ResponseWriter, r *http.Request, coll string) {
	body, ok := decodeQueryBody(w, r)
	if !ok {
		return
	}

	stmt, perr := cosmossql.Parse(body.Query, body.paramMap())
	if perr != nil {
		writeErr(w, perr)
		return
	}

	// Fetch the whole container and evaluate the query here with full fidelity.
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: coll, Limit: allResults})
	if err != nil {
		writeErr(w, err)
		return
	}

	matched, ferr := cosmosFilter(result.Items, stmt.Where)
	if ferr != nil {
		writeErr(w, ferr)
		return
	}

	docs := shapeCosmos(matched, stmt)

	// Page the logical result set with the x-ms-continuation offset contract.
	page, next := pageDocs(docs, continuationOffset(r.Header.Get("X-Ms-Continuation")), maxItemCount(r))
	if next > 0 {
		w.Header().Set("X-Ms-Continuation", strconv.Itoa(next))
	}

	writeJSON(w, http.StatusOK, documentsList{RID: "cloudemu", Documents: page, Count: len(page)})
}

func (h *Handler) documentResource(w http.ResponseWriter, r *http.Request, db, coll, id string) {
	coll = qualify(db, coll)

	cfg, derr := h.db.DescribeTable(r.Context(), coll)
	if derr != nil {
		writeErr(w, derr)
		return
	}

	pk := docPartitionKey(r)
	keyMap := buildKey(cfg.PartitionKey, pk, id)

	switch r.Method {
	case http.MethodGet:
		item, err := h.db.GetItem(r.Context(), coll, keyMap)
		if err != nil {
			writeErr(w, err)
			return
		}

		addSystemProps(item)
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		// Replace document.
		item, ok := decodeAnyJSON(w, r)
		if !ok {
			return
		}

		if _, exists := item[idAttr]; !exists {
			item[idAttr] = id
		}

		if err := h.db.PutItem(r.Context(), coll, item); err != nil {
			writeErr(w, err)
			return
		}

		addSystemProps(item)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := h.db.DeleteItem(r.Context(), coll, keyMap); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// isQuery returns true when the request is a Cosmos query (POST /docs with
// the documentdb-isquery flag).
func isQuery(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("X-Ms-Documentdb-Isquery"), "true") {
		return true
	}
	// The SDK marks continuation-page requests with the query media type
	// rather than repeating the isquery header.
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/query+json")
}

// docPartitionKey extracts the partition-key value from the
// x-ms-documentdb-partitionkey header. Real Cosmos requires this on every
// per-document request. The header value is a JSON array, e.g. `["pk-value"]`.
func docPartitionKey(r *http.Request) string {
	raw := r.Header.Get("X-Ms-Documentdb-Partitionkey")
	if raw == "" {
		return ""
	}

	var parsed []any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ""
	}

	if len(parsed) == 0 {
		return ""
	}

	if s, ok := parsed[0].(string); ok {
		return s
	}

	return fmt.Sprintf("%v", parsed[0])
}

// buildKey constructs the key map the driver expects to look up a document by
// its (partition key, id) identity. pkAttr is the container's declared
// partition-key attribute name (e.g. "pk", "category"); pkVal is the value from
// the x-ms-documentdb-partitionkey header. When the partition key is /id (or
// unset) the id alone identifies the document.
func buildKey(pkAttr, pkVal, id string) map[string]any {
	key := map[string]any{idAttr: id}
	if pkAttr != "" && pkAttr != idAttr {
		key[pkAttr] = pkVal
	}

	return key
}

// isUpsert reports whether a document write carries the Cosmos upsert flag,
// which turns a create (POST /docs) into a create-or-replace and suppresses the
// duplicate-id conflict.
func isUpsert(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Ms-Documentdb-Is-Upsert"), "true")
}

func partitionKeyAttribute(pk *partitionKeyDef) string {
	if pk == nil || len(pk.Paths) == 0 {
		return idAttr
	}

	// Cosmos paths look like "/myKey" — strip the leading slash.
	return strings.TrimPrefix(pk.Paths[0], "/")
}

func defaultPartitionKey() *partitionKeyDef {
	return &partitionKeyDef{Paths: []string{"/id"}, Kind: "Hash"}
}

func makeDatabaseResource(id string) databaseResource {
	rid := "rid-" + id

	return databaseResource{
		resource: resource{
			ID:    id,
			RID:   rid,
			Self:  "dbs/" + rid + "/",
			ETag:  `"` + rid + `"`,
			TS:    time.Now().Unix(),
			Attac: "attachments/",
		},
		Colls: "colls/",
		Users: "users/",
	}
}

func makeContainerResource(db, id string, pk *partitionKeyDef) containerResource {
	// The container's _rid doubles as its offer resource id: the SDK reads _rid
	// off the container, then queries /offers by it. Qualifying by database keeps
	// the offer key unique across databases (see recordOffer / qualify).
	rid := containerRID(qualify(db, id))

	if pk == nil {
		pk = defaultPartitionKey()
	}

	return containerResource{
		resource: resource{
			ID:    id,
			RID:   rid,
			Self:  "dbs/cloudemu/colls/" + rid + "/",
			ETag:  `"` + rid + `"`,
			TS:    time.Now().Unix(),
			Attac: "attachments/",
		},
		Docs:         "docs/",
		Sprocs:       "sprocs/",
		Triggers:     "triggers/",
		UDFs:         "udfs/",
		Conflicts:    "conflicts/",
		PartitionKey: pk,
	}
}

func addSystemProps(item map[string]any) {
	if item == nil {
		return
	}

	id, _ := item[idAttr].(string)

	if _, ok := item["_rid"]; !ok {
		item["_rid"] = "rid-" + id
	}

	if _, ok := item["_self"]; !ok {
		item["_self"] = "dbs/cloudemu/colls/c/docs/" + id
	}

	if _, ok := item["_etag"]; !ok {
		item["_etag"] = `"` + id + `"`
	}

	if _, ok := item["_ts"]; !ok {
		item["_ts"] = time.Now().Unix()
	}

	if _, ok := item["_attachments"]; !ok {
		item["_attachments"] = "attachments/"
	}
}

// JSON helpers.

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func decodeAnyJSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	out := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON: "+err.Error())
		return nil, false
	}

	return out, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorEnvelope{Code: code, Message: msg})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "Conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "BadRequest", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}
