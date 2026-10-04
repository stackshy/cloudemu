// Package tablestorage implements the Azure Table Storage data-plane REST protocol
// (OData/JSON) as a server.Handler. Real azure-sdk-for-go aztables clients
// configured with a custom service URL hit this handler the same way they hit
// {account}.table.core.windows.net.
//
// Supported operations:
//
//	POST   /Tables                                        : create table
//	GET    /Tables                                        : list tables
//	DELETE /Tables('name')                                : delete table
//	POST   /{table}                                       : insert entity
//	GET    /{table}(PartitionKey='p',RowKey='r')          : get entity
//	PUT    /{table}(PartitionKey='p',RowKey='r')          : replace entity
//	MERGE|PATCH /{table}(PartitionKey='p',RowKey='r')     : merge entity
//	DELETE /{table}(PartitionKey='p',RowKey='r')          : delete entity
//	GET    /{table}()?$filter=…                           : query entities
//	POST   /$batch                                        : entity group transaction
//
// Access policies are out of scope. Upsert (a PUT or MERGE with no If-Match
// header against a missing row) is supported as an insert-or-replace/merge,
// matching aztables UpsertEntity.
package tablestorage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

const (
	contentTypeJSON = "application/json"

	// xmsVersion is the Table Storage service version we report.
	xmsVersion = "2019-02-02"

	// maxBodyBytes caps request bodies (entities / table-create payloads).
	maxBodyBytes = 1 << 20

	// etagProp is the system property carrying an entity's ETag on read.
	etagProp = "odata.etag"

	// pathBatch is the entity-group-transaction endpoint path segment.
	pathBatch = "$batch"

	// pathTables is the table lifecycle collection path.
	pathTables = "Tables"
)

// Handler serves Azure Table Storage REST requests against a TableStorage
// driver.
type Handler struct {
	ts       driver.TableStorage
	accounts storagedriver.AzureStorageAccounts
}

// New returns a Table handler backed by ts.
func New(ts driver.TableStorage) *Handler {
	return &Handler{ts: ts}
}

// Matches returns true for Azure Table Storage data-plane requests. The
// detection signals are disjoint from every other Azure handler:
//
//   - The path is /Tables or /Tables('name'): the table lifecycle surface,
//     which no other service uses.
//
//   - The path's first segment carries an OData key predicate: it contains a
//     "(": either "()" (query entities) or
//     "(PartitionKey='…',RowKey='…')" (entity CRUD). Blob/Queue paths never
//     contain parentheses, and ARM paths start with /subscriptions/, so this
//     is unambiguous.
//
//   - POST /{table} (insert entity) is a bare single segment with a JSON body.
//     Blob and Queue never use a bare POST on a single path segment, so the
//     method+content-type discriminates it from their PUT/DELETE ops.
//
//   - The account-level service calls (?restype=service|account) on the root,
//     on an {account}.table host or, on a bare host, from a Table client.
//
// A path-style /{account}/ prefix naming an existing storage account is
// peeled before the shape checks (see resolve).
//
// Registered before the permissive Blob fallback so these shapes win.
func (h *Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/subscriptions/") {
		return false
	}

	// A storage host names its service, so a request to another service's
	// host (such as {account}.blob.core.windows.net) is never a Table call.
	_, svc, storageHost := azurearm.StorageHost(r.Host)
	if storageHost && svc != azurearm.StorageServiceTable {
		return false
	}

	_, resolved := h.resolve(r)
	path := strings.TrimPrefix(resolved, "/")

	if path == "" {
		return azurearm.IsStorageServiceOp(r.URL.Query()) && (storageHost || isTableClient(r))
	}

	return matchesTablePath(r, path)
}

// matchesTablePath reports whether a path below the account is a Table call.
func matchesTablePath(r *http.Request, path string) bool {
	// /$batch (entity group transactions), /Tables and /Tables('name').
	if path == pathBatch || path == pathTables || strings.HasPrefix(path, "Tables(") {
		return true
	}

	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}

	// Entity CRUD / query: first path segment contains an OData "(" predicate.
	if strings.ContainsRune(first, '(') {
		return true
	}

	// Insert entity: POST /{table} with a JSON body.
	if r.Method == http.MethodPost && first != "" && !strings.Contains(first, "/") {
		return isJSONContentType(r.Header.Get("Content-Type"))
	}

	return false
}

func isJSONContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "application/json")
}

// ServeHTTP routes on the parsed path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Ms-Version", xmsVersion)
	w.Header().Set("Dataserviceversion", "3.0")

	account, resolved := h.resolve(r)
	path := strings.TrimPrefix(resolved, "/")

	switch {
	case path == "" && azurearm.IsStorageServiceOp(r.URL.Query()):
		azurearm.ServeStorageServiceOp(w, r)
	case path == pathBatch:
		h.batch(w, r, account)
	case path == pathTables:
		h.tablesCollectionOp(w, r, account)
	case strings.HasPrefix(path, "Tables("):
		h.deleteTable(w, r, tableKey(account, tableNameFromDelete(path)))
	case r.Method == http.MethodPost && !strings.ContainsRune(path, '('):
		// POST /{table}: insert entity into a bare table path.
		h.insertEntity(w, r, tableKey(account, path))
	default:
		h.entityOp(w, r, account, path)
	}
}

// tablesCollectionOp handles POST (create) and GET (list) on /Tables.
func (h *Handler) tablesCollectionOp(w http.ResponseWriter, r *http.Request, account string) {
	switch r.Method {
	case http.MethodPost:
		h.createTable(w, r, account)
	case http.MethodGet:
		h.listTables(w, r, account)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request, account string) {
	var body struct {
		TableName string `json:"TableName"`
	}

	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "malformed request body")
		return
	}

	if err := h.ts.CreateTable(r.Context(), tableKey(account, body.TableName)); err != nil {
		// A duplicate table is TableAlreadyExists, distinct from the generic
		// EntityAlreadyExists that a duplicate entity insert reports.
		if cerrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "TableAlreadyExists", cerrors.Message(err))
			return
		}

		writeErr(w, err)

		return
	}

	resp := map[string]any{
		"odata.metadata": fmt.Sprintf("%s://%s/$metadata#Tables/@Element", scheme(r), r.Host),
		"TableName":      body.TableName,
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) listTables(w http.ResponseWriter, r *http.Request, account string) {
	names, err := h.ts.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	value := make([]map[string]any, 0, len(names))

	for _, n := range names {
		if acct, name := storagedriver.SplitAzureContainerKey(n); acct == account {
			value = append(value, map[string]any{"TableName": name})
		}
	}

	resp := map[string]any{
		"odata.metadata": fmt.Sprintf("%s://%s/$metadata#Tables", scheme(r), r.Host),
		"value":          value,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	if err := h.ts.DeleteTable(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// entityOp routes entity-level requests: /{table}() (query) and
// /{table}(PartitionKey='p',RowKey='r') (CRUD).
func (h *Handler) entityOp(w http.ResponseWriter, r *http.Request, account, path string) {
	table, predicate, ok := splitEntityPath(path)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidUri", "unrecognized table path")
		return
	}

	table = tableKey(account, table)

	// Query: /{table}() with an empty predicate.
	if strings.TrimSpace(predicate) == "" {
		h.queryEntities(w, r, table)
		return
	}

	pk, rk, ok := parseKeyPredicate(predicate)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidInput", "malformed entity key predicate")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getEntity(w, r, table, pk, rk)
	case http.MethodPut:
		h.updateEntity(w, r, table, pk, rk, driver.UpdateModeReplace)
	case http.MethodPatch, methodMerge:
		h.updateEntity(w, r, table, pk, rk, driver.UpdateModeMerge)
	case http.MethodDelete:
		h.deleteEntity(w, r, table, pk, rk)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) queryEntities(w http.ResponseWriter, r *http.Request, table string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	q := r.URL.Query()

	res, err := h.ts.QueryEntities(r.Context(), table, driver.QueryOptions{
		Filter:           q.Get("$filter"),
		Top:              atoiDefault(q.Get("$top"), 0),
		Select:           q.Get("$select"),
		NextPartitionKey: q.Get("NextPartitionKey"),
		NextRowKey:       q.Get("NextRowKey"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	value := make([]map[string]any, 0, len(res.Entities))
	for _, e := range res.Entities {
		value = append(value, entityToJSON(e))
	}

	// Continuation tokens travel as response headers, mirroring the real
	// service; aztables' pager reads them to fetch the next page.
	if res.NextPartitionKey != "" || res.NextRowKey != "" {
		w.Header().Set("X-Ms-Continuation-Nextpartitionkey", res.NextPartitionKey)
		w.Header().Set("X-Ms-Continuation-Nextrowkey", res.NextRowKey)
	}

	resp := map[string]any{
		"odata.metadata": fmt.Sprintf("%s://%s/$metadata#%s", scheme(r), r.Host, tableName(table)),
		"value":          value,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getEntity(w http.ResponseWriter, r *http.Request, table, pk, rk string) {
	ent, err := h.ts.GetEntity(r.Context(), table, pk, rk)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := entityToJSON(ent)
	out["odata.metadata"] = fmt.Sprintf("%s://%s/$metadata#%s/@Element", scheme(r), r.Host, tableName(table))

	if etag := asString(ent[etagProp]); etag != "" {
		w.Header().Set("ETag", etag)
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) insertEntity(w http.ResponseWriter, r *http.Request, table string) {
	ent, err := readEntity(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "malformed entity body")
		return
	}

	pk := asString(ent["PartitionKey"])
	rk := asString(ent["RowKey"])

	etag, err := h.ts.InsertEntity(r.Context(), table, pk, rk, driver.Entity(ent))
	if err != nil {
		writeErr(w, err)
		return
	}

	// Default (no Prefer header): return-content, echoing the entity with 201.
	out := entityToJSON(ent)
	out["odata.metadata"] = fmt.Sprintf("%s://%s/$metadata#%s/@Element", scheme(r), r.Host, tableName(table))
	out[etagProp] = etag

	w.Header().Set("ETag", etag)

	if strings.EqualFold(r.Header.Get("Prefer"), "return-no-content") {
		w.Header().Set("Preference-Applied", "return-no-content")
		w.WriteHeader(http.StatusNoContent)

		return
	}

	w.Header().Set("Preference-Applied", "return-content")
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) updateEntity(
	w http.ResponseWriter, r *http.Request, table, pk, rk string, mode driver.UpdateMode,
) {
	ent, err := readEntity(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "malformed entity body")
		return
	}

	// The key comes from the URL; ensure it's present in the stored entity.
	ent["PartitionKey"] = pk
	ent["RowKey"] = rk

	ifMatch := r.Header.Get("If-Match")

	etag, err := h.ts.UpdateEntity(r.Context(), table, pk, rk, driver.Entity(ent), mode, ifMatch)
	if err != nil {
		// aztables UpdateEntity sends an If-Match header; UpsertEntity does not.
		// With no If-Match, a PUT/MERGE against a missing row is an insert
		// (insert-or-replace/merge), so create it instead of returning 404.
		if cerrors.IsNotFound(err) && ifMatch == "" {
			ietag, ierr := h.ts.InsertEntity(r.Context(), table, pk, rk, driver.Entity(ent))
			if ierr != nil {
				writeErr(w, ierr)
				return
			}

			w.Header().Set("ETag", ietag)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		writeErr(w, err)

		return
	}

	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteEntity(w http.ResponseWriter, r *http.Request, table, pk, rk string) {
	if err := h.ts.DeleteEntity(r.Context(), table, pk, rk, r.Header.Get("If-Match")); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func readEntity(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}

	var ent map[string]any
	if err := json.Unmarshal(data, &ent); err != nil {
		return nil, err
	}

	return ent, nil
}
