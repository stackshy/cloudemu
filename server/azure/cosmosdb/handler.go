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

	// databases tracks the Cosmos databases that exist, keyed by their
	// account-qualified identity (see dbNS). The generic driver has a flat table
	// namespace with no account or database grouping, so the wire handler models
	// both layers: it records which databases were created and qualifies every
	// container's driver-table name by its account and database (see qualify),
	// giving each account an isolated database/container namespace.
	dbMu      sync.RWMutex
	databases map[string]struct{}

	// attrs tracks the Cosmos-only container properties the generic driver has
	// no concept of (default TTL, unique key policy) plus the per-item TTL
	// bookkeeping needed to enforce it. See container_attrs.go.
	attrs *attrsStore

	// writeMu serializes item mutations and TTL reaping per container. A create's
	// uniqueness check and its insert must be one uninterruptible step (see
	// keyedMutex), and a lazy TTL sweep must not race a concurrent write.
	writeMu *keyedMutex
}

// New returns a Cosmos handler backed by db.
func New(db dbdriver.Database) *Handler {
	return &Handler{
		db:        db,
		offers:    make(map[string]offerState),
		databases: make(map[string]struct{}),
		attrs:     newAttrsStore(),
		writeMu:   newKeyedMutex(),
	}
}

// nsPrefix returns the driver-table namespace prefix for an account. The
// default account (empty name — the legacy single-account and official-emulator
// "https://host:port/" usage) has no prefix, so its tables keep the historical
// "{db}/{coll}" names; a named account (addressed via the
// {account}.documents.azure.com host, modeled here as a leading /{account} path
// segment) prefixes every table with "{account}/", giving each ARM-created
// account an isolated namespace.
func nsPrefix(account string) string {
	if account == "" {
		return ""
	}

	return account + "/"
}

// qualify maps an (account, database, container) triple onto the flat driver
// table name that backs it. Account names and Cosmos ids cannot contain '/', so
// "{account}/{db}/{coll}" is an unambiguous, collision-free key: a container is
// reachable only through its own account and database, and an account's or
// database's tables can be found by prefix.
func qualify(account, db, coll string) string {
	return nsPrefix(account) + db + "/" + coll
}

// dbNS is the account-qualified database identity: the databases-set key, the
// database's shared-throughput offer key, and the prefix under which
// deleteDatabase reaps the database's container tables.
func dbNS(account, db string) string {
	return nsPrefix(account) + db
}

// databaseExists reports whether database db has been created in account.
func (h *Handler) databaseExists(account, db string) bool {
	h.dbMu.RLock()
	_, ok := h.databases[dbNS(account, db)]
	h.dbMu.RUnlock()

	return ok
}

// splitAccount separates an optional leading /{account} segment from the Cosmos
// resource path. The real azcosmos SDK derives the account from the
// {account}.documents.azure.com host; the emulator collapses every account onto
// one listener, so the ARM control plane hands clients a documentEndpoint whose
// path carries the account ("https://host/{account}/"), and the SDK preserves
// that prefix on every data-plane request (on failover it swaps only the Host).
// A request with no such prefix ("/dbs/...", "/offers", "/") targets the default
// account, keeping single-account and official-emulator usage working. The
// returned rest is the remaining path with the account segment removed ("/" for
// a bare account probe).
//
// The leading segment is peeled as an account ONLY when it names a Cosmos
// databaseAccount actually registered through the shared ARM control plane
// (isAccount). Any other first segment — "dbs"/"offers" of the default account,
// or a blob container/virtual-directory prefix when blob and cosmos share one
// listener — is left unpeeled so Matches declines it and the request falls
// through to the blob handler. This keeps an account literally named "dbs" or
// "offers" reachable while never stealing a blob path.
func (h *Handler) splitAccount(p string) (account, rest string) {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return "", "/"
	}

	first := trimmed
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		first = trimmed[:i]
	}

	if !h.isAccount(first) {
		return "", p
	}

	rest = strings.TrimPrefix(p, "/"+first)
	if rest == "" {
		rest = "/"
	}

	return first, rest
}

// accountLister is the optional capability the shared database driver exposes to
// enumerate the Cosmos databaseAccounts registered through the ARM control
// plane (providers/azure/cosmosdb implements it; DynamoDB/Firestore don't).
type accountLister interface {
	AccountTables() []string
}

// isAccount reports whether name is a registered Cosmos databaseAccount. The
// data-plane handler shares the very driver the account control plane writes to,
// so it can distinguish a real account prefix from an unrelated leading segment
// (a blob container, a default-account "dbs"/"offers") and only peel the former.
func (h *Handler) isAccount(name string) bool {
	if name == "" {
		return false
	}

	lister, ok := h.db.(accountLister)
	if !ok {
		return false
	}

	for _, a := range lister.AccountTables() {
		if a == name {
			return true
		}
	}

	return false
}

// Matches returns true for the Cosmos data plane URLs we serve: the account
// root probe (GET / or GET /{account}), the /dbs/... resource tree, and the
// /offers throughput resource — each optionally under a /{account} prefix.
func (h *Handler) Matches(r *http.Request) bool {
	account, rest := h.splitAccount(r.URL.Path)

	// A bare "/{account}" carrying a query string is more likely a blob
	// container/root operation than a Cosmos account probe (which carries none),
	// so decline it and let the blob handler, registered after this one, serve it.
	if account != "" && rest == "/" && r.URL.RawQuery != "" {
		return false
	}

	return rest == "/" || rest == "/dbs" || strings.HasPrefix(rest, "/dbs/") ||
		rest == offersPath || strings.HasPrefix(rest, offersPathPrefix)
}

// ServeHTTP routes the request based on URL path shape, after peeling off the
// optional /{account} prefix that scopes the data plane to one account.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	account, rest := h.splitAccount(r.URL.Path)

	if rest == "/" {
		h.accountProperties(w, r)
		return
	}

	if rest == offersPath || strings.HasPrefix(rest, offersPathPrefix) {
		h.serveOffers(w, r, rest)
		return
	}

	parts := strings.Split(strings.Trim(rest, "/"), "/")

	switch len(parts) {
	case 1:
		// /dbs
		h.databaseCollection(w, r, account)
	case pathDBOnly:
		// /dbs/{db}
		h.databaseResource(w, r, account, parts[1])
	case pathColls:
		// /dbs/{db}/colls
		h.containerCollection(w, r, account, parts[1])
	case pathContainerOrDocsCol:
		// /dbs/{db}/colls/{coll}
		h.containerResource(w, r, account, parts[1], parts[3])
	case pathDocsCol:
		// /dbs/{db}/colls/{coll}/docs
		h.documentCollection(w, r, account, parts[1], parts[3])
	case pathDocResource:
		// /dbs/{db}/colls/{coll}/docs/{id}
		h.documentResource(w, r, account, parts[1], parts[3], parts[5])
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

func (h *Handler) databaseCollection(w http.ResponseWriter, r *http.Request, account string) {
	switch r.Method {
	case http.MethodPost:
		// Cosmos overloads POST /dbs: a create (JSON body with an id) or, when the
		// isquery flag is set, a query the SDK's database pager fires. Drain the
		// query body but ignore its predicate — we list all databases.
		if isQuery(r) {
			_, _ = decodeQueryBody(w, r)
			h.writeDatabaseList(w, account)

			return
		}

		h.createDatabase(w, r, account)
	case http.MethodGet:
		h.writeDatabaseList(w, account)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request, account string) {
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

	key := dbNS(account, body.ID)

	h.dbMu.Lock()
	if _, exists := h.databases[key]; exists {
		h.dbMu.Unlock()
		writeError(w, http.StatusConflict, "Conflict",
			"Database with specified id already exists.")

		return
	}

	h.databases[key] = struct{}{}
	h.dbMu.Unlock()

	// A database created with ThroughputProperties (shared, database-level
	// provisioned throughput) gets its own offer, keyed by the same _rid
	// makeDatabaseResource assigns it — recordOffer's containerRID(dbNS) derives
	// exactly that "rid-{account-qualified-db}" key, so ReadThroughput round-trips
	// without a container ever having been created.
	h.recordOffer(key, r)

	writeJSON(w, http.StatusCreated, makeDatabaseResource(account, body.ID))
}

func (h *Handler) writeDatabaseList(w http.ResponseWriter, account string) {
	h.dbMu.RLock()
	names := make([]string, 0, len(h.databases))

	for key := range h.databases {
		// Only list databases in this account's namespace. The default account's
		// keys ("{db}") must not leak into a named account and vice versa.
		id, ok := accountDBName(key, account)
		if !ok {
			continue
		}

		names = append(names, id)
	}

	h.dbMu.RUnlock()
	sort.Strings(names)

	list := databasesList{RID: "cloudemu", Databases: make([]databaseResource, 0, len(names))}
	for _, name := range names {
		list.Databases = append(list.Databases, makeDatabaseResource(account, name))
	}

	list.Count = len(list.Databases)
	writeJSON(w, http.StatusOK, list)
}

// accountDBName reports whether databases-set key belongs to account and, if so,
// returns the bare database id. A named account owns keys "{account}/{db}"; the
// default account owns keys "{db}" that carry no further '/'.
func accountDBName(key, account string) (string, bool) {
	if account == "" {
		if strings.ContainsRune(key, '/') {
			return "", false
		}

		return key, true
	}

	return strings.CutPrefix(key, account+"/")
}

func (h *Handler) databaseResource(w http.ResponseWriter, r *http.Request, account, db string) {
	switch r.Method {
	case http.MethodGet:
		if !h.databaseExists(account, db) {
			writeError(w, http.StatusNotFound, "NotFound", "database not found")
			return
		}

		writeJSON(w, http.StatusOK, makeDatabaseResource(account, db))
	case http.MethodDelete:
		h.deleteDatabase(w, r, account, db)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// deleteDatabase removes a database and every container inside it. Because the
// driver namespace is flat and keyed by qualify(account, db, coll), the
// database's containers are exactly the tables whose name starts with the
// account-qualified "{ns}/" prefix.
func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request, account, db string) {
	if !h.databaseExists(account, db) {
		writeError(w, http.StatusNotFound, "NotFound", "database not found")
		return
	}

	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	ns := dbNS(account, db)
	prefix := ns + "/"

	for _, t := range tables {
		if !strings.HasPrefix(t, prefix) {
			continue
		}

		if derr := h.db.DeleteTable(r.Context(), t); derr != nil {
			writeErr(w, derr)
			return
		}

		h.deleteOffer(t)
		h.attrs.delete(t)
	}

	h.deleteOffer(ns) // the database's own shared-throughput offer, if any
	h.dbMu.Lock()
	delete(h.databases, ns)
	h.dbMu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) containerCollection(w http.ResponseWriter, r *http.Request, account, db string) {
	switch r.Method {
	case http.MethodPost:
		h.createContainer(w, r, account, db)
	case http.MethodGet:
		h.listContainers(w, r, account, db)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createContainer(w http.ResponseWriter, r *http.Request, account, db string) {
	if !h.databaseExists(account, db) {
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
		Name:         qualify(account, db, body.ID),
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

	table := qualify(account, db, body.ID)
	h.recordOffer(table, r)
	h.attrs.set(table, body.DefaultTTL, body.UniqueKeyPolicy, body.IndexingPolicy)

	writeJSON(w, http.StatusCreated,
		makeContainerResource(account, db, body.ID, body.PartitionKey, body.DefaultTTL, body.UniqueKeyPolicy, body.IndexingPolicy))
}

func (h *Handler) listContainers(w http.ResponseWriter, r *http.Request, account, db string) {
	if !h.databaseExists(account, db) {
		writeError(w, http.StatusNotFound, "NotFound", "database not found")
		return
	}

	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := containersList{RID: "cloudemu"}
	prefix := dbNS(account, db) + "/"

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

		attrs := h.attrs.get(t)
		out.DocumentCollections = append(out.DocumentCollections,
			makeContainerResource(account, db, coll, pk, attrs.defaultTTL,
				uniqueKeyPolicyFromDef(attrs.uniqueKeys), attrs.indexingPolicy))
	}

	out.Count = len(out.DocumentCollections)

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) containerResource(w http.ResponseWriter, r *http.Request, account, db, coll string) {
	table := qualify(account, db, coll)

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

		attrs := h.attrs.get(table)
		writeJSON(w, http.StatusOK,
			makeContainerResource(account, db, coll, pk, attrs.defaultTTL,
				uniqueKeyPolicyFromDef(attrs.uniqueKeys), attrs.indexingPolicy))
	case http.MethodDelete:
		if err := h.db.DeleteTable(r.Context(), table); err != nil {
			writeErr(w, err)
			return
		}

		h.deleteOffer(table)
		h.attrs.delete(table)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) documentCollection(w http.ResponseWriter, r *http.Request, account, db, coll string) {
	table := qualify(account, db, coll)

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

	if err := h.insertDocument(r.Context(), coll, cfg, item, isUpsert(r)); err != nil {
		writeErr(w, err)
		return
	}

	addSystemProps(item)
	writeJSON(w, http.StatusCreated, item)
}

// insertDocument performs the duplicate-id check, unique-key check and PutItem
// as one atomic step under the container's write lock, so two concurrent
// creates carrying the same (partition, unique-key value) cannot both pass the
// checks and both insert. On upsert the duplicate-id check is skipped (create
// becomes create-or-replace). Conflicts are returned as AlreadyExists so the
// wire layer maps them to a 409.
func (h *Handler) insertDocument(
	ctx context.Context, coll string, cfg *dbdriver.TableConfig, item map[string]any, upsert bool,
) error {
	unlock := h.writeMu.lock(coll)
	defer unlock()

	// A plain create (not an upsert) must fail if a document with the same
	// (partition key, id) already exists, matching real Cosmos's 409 Conflict.
	if !upsert && h.documentExists(ctx, coll, cfg, item) {
		return cerrors.New(cerrors.AlreadyExists, "Resource with specified id or name already exists.")
	}

	if err := h.checkUniqueKeys(ctx, coll, cfg, item); err != nil {
		return err
	}

	if err := h.db.PutItem(ctx, coll, item); err != nil {
		return err
	}

	h.attrs.recordWrite(coll, cfg, item)

	return nil
}

// replaceDocument overwrites a document and refreshes its TTL bookkeeping under
// the container's write lock, so a concurrent TTL reap cannot delete the item
// between the write and the expiry update.
func (h *Handler) replaceDocument(ctx context.Context, coll string, cfg *dbdriver.TableConfig, item map[string]any) error {
	unlock := h.writeMu.lock(coll)
	defer unlock()

	if err := h.db.PutItem(ctx, coll, item); err != nil {
		return err
	}

	h.attrs.recordWrite(coll, cfg, item)

	return nil
}

// deleteDocument removes a document and its TTL bookkeeping under the
// container's write lock, keeping the delete and forget serialized against
// creates, replaces and TTL reaps.
func (h *Handler) deleteDocument(ctx context.Context, coll string, cfg *dbdriver.TableConfig, keyMap map[string]any) error {
	unlock := h.writeMu.lock(coll)
	defer unlock()

	if err := h.db.DeleteItem(ctx, coll, keyMap); err != nil {
		return err
	}

	h.attrs.forget(coll, cfg, keyMap)

	return nil
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

// readDocument serves the GET case of documentResource: a point read that
// also enforces per-item TTL, lazily reaping an expired document the same way
// a real Cosmos TTL background sweep would already have removed it.
func (h *Handler) readDocument(w http.ResponseWriter, r *http.Request, coll string, cfg *dbdriver.TableConfig, keyMap map[string]any) {
	item, expired, err := h.pointRead(r.Context(), coll, cfg, keyMap)
	if err != nil {
		writeErr(w, err)
		return
	}

	if expired {
		writeError(w, http.StatusNotFound, "NotFound", "item not found")
		return
	}

	addSystemProps(item)
	writeJSON(w, http.StatusOK, item)
}

// pointRead fetches a document and, when its TTL has elapsed, reaps it under the
// container's write lock (reporting expired=true) so the read-time expiry check
// and the delete are atomic against a concurrent create/replace.
func (h *Handler) pointRead(
	ctx context.Context, coll string, cfg *dbdriver.TableConfig, keyMap map[string]any,
) (item map[string]any, expired bool, err error) {
	unlock := h.writeMu.lock(coll)
	defer unlock()

	item, err = h.db.GetItem(ctx, coll, keyMap)
	if err != nil {
		return nil, false, err
	}

	if h.attrs.expired(coll, cfg, item) {
		h.reapExpired(ctx, coll, cfg, item)
		return nil, true, nil
	}

	return item, false, nil
}

// dropExpired filters out items whose TTL has elapsed AND reaps them from the
// store — deleting the document and forgetting its TTL bookkeeping — matching a
// real Cosmos TTL background sweep, which does not merely hide expired items but
// removes them. Reaping runs under the container's write lock so a document a
// concurrent create/replace just wrote is never mistaken for the expired one
// and deleted.
func (h *Handler) dropExpired(ctx context.Context, coll string, items []map[string]any) []map[string]any {
	cfg, err := h.db.DescribeTable(ctx, coll)
	if err != nil || cfg == nil {
		return items
	}

	unlock := h.writeMu.lock(coll)
	defer unlock()

	out := make([]map[string]any, 0, len(items))

	for _, it := range items {
		if h.attrs.expired(coll, cfg, it) {
			h.reapExpired(ctx, coll, cfg, it)
			continue
		}

		out = append(out, it)
	}

	return out
}

// reapExpired deletes an expired document from the store and forgets its TTL
// bookkeeping. The caller must already hold the container's write lock.
func (h *Handler) reapExpired(ctx context.Context, coll string, cfg *dbdriver.TableConfig, item map[string]any) {
	key := map[string]any{idAttr: item[idAttr]}
	if cfg.PartitionKey != "" && cfg.PartitionKey != idAttr {
		key[cfg.PartitionKey] = item[cfg.PartitionKey]
	}

	_ = h.db.DeleteItem(ctx, coll, key)
	h.attrs.forget(coll, cfg, item)
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

	items := h.dropExpired(r.Context(), coll, result.Items)

	docs := make([]any, 0, len(items))

	for i := range items {
		addSystemProps(items[i])
		docs = append(docs, items[i])
	}

	if result.NextPageToken != "" {
		w.Header().Set("X-Ms-Continuation", result.NextPageToken)
	}

	writeJSON(w, http.StatusOK, documentsList{
		RID:       "cloudemu",
		Documents: docs,
		Count:     len(docs),
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

	items := h.dropExpired(r.Context(), coll, result.Items)

	matched, ferr := cosmosFilter(items, stmt.Where)
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

func (h *Handler) documentResource(w http.ResponseWriter, r *http.Request, account, db, coll, id string) {
	coll = qualify(account, db, coll)

	cfg, derr := h.db.DescribeTable(r.Context(), coll)
	if derr != nil {
		writeErr(w, derr)
		return
	}

	pk := docPartitionKey(r)
	keyMap := buildKey(cfg.PartitionKey, pk, id)

	switch r.Method {
	case http.MethodGet:
		h.readDocument(w, r, coll, cfg, keyMap)
	case http.MethodPut:
		// Replace document.
		item, ok := decodeAnyJSON(w, r)
		if !ok {
			return
		}

		if _, exists := item[idAttr]; !exists {
			item[idAttr] = id
		}

		if err := h.replaceDocument(r.Context(), coll, cfg, item); err != nil {
			writeErr(w, err)
			return
		}

		addSystemProps(item)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := h.deleteDocument(r.Context(), coll, cfg, keyMap); err != nil {
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

func makeDatabaseResource(account, id string) databaseResource {
	// The _rid doubles as the database's offer resource id (see createDatabase /
	// containerRID). Qualifying by account keeps it unique across accounts so a
	// shared-throughput offer never collides between two accounts' same-named
	// databases; the displayed id stays the bare database name.
	rid := "rid-" + dbNS(account, id)

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

func makeContainerResource(
	account, db, id string, pk *partitionKeyDef, defaultTTL *int32, uk *uniqueKeyPolicy, indexing map[string]any,
) containerResource {
	// The container's _rid doubles as its offer resource id: the SDK reads _rid
	// off the container, then queries /offers by it. Qualifying by account and
	// database keeps the offer key unique across both (see recordOffer / qualify).
	rid := containerRID(qualify(account, db, id))

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
		Docs:            "docs/",
		Sprocs:          "sprocs/",
		Triggers:        "triggers/",
		UDFs:            "udfs/",
		Conflicts:       "conflicts/",
		PartitionKey:    pk,
		IndexingPolicy:  indexing,
		DefaultTTL:      defaultTTL,
		UniqueKeyPolicy: uk,
	}
}

// uniqueKeyPolicyFromDef re-wraps the attrsStore's flat []uniqueKeyDef back
// into the wire uniqueKeyPolicy shape for a Read/List response; nil when the
// container declared no unique keys, so the field is omitted rather than
// echoed as an empty policy.
func uniqueKeyPolicyFromDef(keys []uniqueKeyDef) *uniqueKeyPolicy {
	if len(keys) == 0 {
		return nil
	}

	return &uniqueKeyPolicy{UniqueKeys: keys}
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
