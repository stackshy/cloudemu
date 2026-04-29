// Package cosmos implements the Azure Cosmos DB SQL data-plane REST API
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
// The driver doesn't model Cosmos's database-level grouping, so we expose a
// single virtual database "cloudemu" that always exists and contains every
// driver table as a container.
package cosmos

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dbdriver "github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// Handler serves Cosmos DB SQL API requests against a database driver.
type Handler struct {
	db dbdriver.Database
}

// New returns a Cosmos handler backed by db.
func New(db dbdriver.Database) *Handler {
	return &Handler{db: db}
}

// Matches returns true for the Cosmos data plane URLs we serve: the account
// root probe (GET /) and the /dbs/... resource tree.
func (*Handler) Matches(r *http.Request) bool {
	return r.URL.Path == "/" || r.URL.Path == "/dbs" || strings.HasPrefix(r.URL.Path, "/dbs/")
}

// ServeHTTP routes the request based on URL path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		h.accountProperties(w, r)
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
		"id":                           "cloudemu",
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

const defaultDBName = "cloudemu"

func (*Handler) databaseCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			ID string `json:"id"`
		}

		if !decodeJSON(w, r, &body) {
			return
		}

		// We pretend any database creation succeeds. Items live under tables;
		// the Cosmos database layer is virtual.
		writeJSON(w, http.StatusCreated, makeDatabaseResource(body.ID))
	case http.MethodGet:
		writeJSON(w, http.StatusOK, databasesList{
			RID: "cloudemu",
			Databases: []databaseResource{
				makeDatabaseResource(defaultDBName),
			},
			Count: 1,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (*Handler) databaseResource(w http.ResponseWriter, r *http.Request, db string) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, makeDatabaseResource(db))
	case http.MethodDelete:
		// No-op; the virtual database can't actually be deleted because
		// tables underneath still belong to the driver.
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
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
	var body containerResource

	if !decodeJSON(w, r, &body) {
		return
	}

	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "BadRequest", "container id required")
		return
	}

	cfg := dbdriver.TableConfig{
		Name:         body.ID,
		PartitionKey: partitionKeyAttribute(body.PartitionKey),
	}

	if err := h.db.CreateTable(r.Context(), cfg); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, makeContainerResource(db, body.ID, body.PartitionKey))
}

func (h *Handler) listContainers(w http.ResponseWriter, r *http.Request, db string) {
	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := containersList{RID: "cloudemu"}

	for _, t := range tables {
		cfg, derr := h.db.DescribeTable(r.Context(), t)
		pk := defaultPartitionKey()

		if derr == nil && cfg != nil && cfg.PartitionKey != "" {
			pk = &partitionKeyDef{Paths: []string{"/" + cfg.PartitionKey}, Kind: "Hash"}
		}

		out.DocumentCollections = append(out.DocumentCollections,
			makeContainerResource(db, t, pk))
	}

	out.Count = len(out.DocumentCollections)

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) containerResource(w http.ResponseWriter, r *http.Request, db, coll string) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.db.DescribeTable(r.Context(), coll)
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
		if err := h.db.DeleteTable(r.Context(), coll); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) documentCollection(w http.ResponseWriter, r *http.Request, db, coll string) {
	switch r.Method {
	case http.MethodPost:
		// Cosmos overloads POST /docs for both create and query depending on
		// the x-ms-documentdb-isquery header.
		if isQuery(r) {
			h.queryDocuments(w, r, coll)
			return
		}

		h.createDocument(w, r, db, coll)
	case http.MethodGet:
		h.listDocuments(w, r, coll)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request, _, coll string) {
	item, ok := decodeAnyJSON(w, r)
	if !ok {
		return
	}

	if _, exists := item["id"]; !exists {
		writeError(w, http.StatusBadRequest, "BadRequest", "item must contain an id field")
		return
	}

	if err := h.db.PutItem(r.Context(), coll, item); err != nil {
		writeErr(w, err)
		return
	}

	addSystemProps(item)
	writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request, coll string) {
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: coll})
	if err != nil {
		writeErr(w, err)
		return
	}

	for i := range result.Items {
		addSystemProps(result.Items[i])
	}

	writeJSON(w, http.StatusOK, documentsList{
		RID:       "cloudemu",
		Documents: result.Items,
		Count:     result.Count,
	})
}

func (h *Handler) queryDocuments(w http.ResponseWriter, r *http.Request, coll string) {
	// We accept the query body but ignore it — return all items via Scan.
	// This is sufficient for SDK round-trip validation; full SQL parsing is
	// out of scope.
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	_ = body

	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: coll})
	if err != nil {
		writeErr(w, err)
		return
	}

	for i := range result.Items {
		addSystemProps(result.Items[i])
	}

	writeJSON(w, http.StatusOK, documentsList{
		RID:       "cloudemu",
		Documents: result.Items,
		Count:     result.Count,
	})
}

func (h *Handler) documentResource(w http.ResponseWriter, r *http.Request, _, coll, id string) {
	pk := docPartitionKey(r)
	keyMap := buildKey(coll, pk, id)

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

		if _, exists := item["id"]; !exists {
			item["id"] = id
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
	return strings.EqualFold(r.Header.Get("X-Ms-Documentdb-Isquery"), "true") ||
		strings.EqualFold(r.Header.Get("X-Ms-Documentdb-Isquery"), "True")
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

// buildKey constructs the key map our driver expects to look up a document.
// The driver uses the partition-key attribute name to find the right item;
// for items where the table has no explicit partition key we fall back to id.
func buildKey(_, pk, id string) map[string]any {
	if pk == "" {
		return map[string]any{"id": id}
	}

	return map[string]any{"id": id, "pk": pk}
}

func partitionKeyAttribute(pk *partitionKeyDef) string {
	if pk == nil || len(pk.Paths) == 0 {
		return "id"
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

func makeContainerResource(_, id string, pk *partitionKeyDef) containerResource {
	rid := "rid-" + id

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

	id, _ := item["id"].(string)

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
