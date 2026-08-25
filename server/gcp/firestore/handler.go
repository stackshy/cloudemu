// Package firestore implements the GCP Firestore REST API as a server.Handler.
// Real cloud.google.com/go/firestore clients constructed via NewRESTClient
// hit this handler the same way they hit firestore.googleapis.com.
//
// Supported operations (parity with AWS DynamoDB):
//
//	POST   /v1/projects/{p}/databases/{db}/documents/{collection}        — create document
//	POST   /v1/projects/{p}/databases/{db}/documents/{collection}?documentId={id}
//	GET    /v1/projects/{p}/databases/{db}/documents/{collection}        — list documents
//	GET    /v1/projects/{p}/databases/{db}/documents/{collection}/{id}   — get document
//	PATCH  /v1/projects/{p}/databases/{db}/documents/{collection}/{id}   — update document
//	DELETE /v1/projects/{p}/databases/{db}/documents/{collection}/{id}   — delete document
package firestore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// Path-segment values used in Firestore REST URLs.
const (
	segProjects  = "projects"
	segDatabases = "databases"
	segDocuments = "documents"
)

// Static sentinel errors so the err113 lint stays satisfied while keeping
// messages readable.
var (
	errNotDocPath     = errStr("not a firestore document path")
	errInvalidDocName = errStr("invalid document name")
	errMissingDocID   = errStr("missing document id in name")
)

// errStr is a string-backed error type so we can declare sentinel errors
// without pulling in errors.New (which trips the err113 linter).
type errStr string

func (e errStr) Error() string { return string(e) }

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20
)

// Reserved item keys the handler stores alongside user fields. The document id
// is the driver partition key; createTime/updateTime carry stable commit
// timestamps. None are surfaced as document fields.
const (
	fieldID         = "id"
	fieldCreateTime = "__createTime__"
	fieldUpdateTime = "__updateTime__"
)

// isReservedKey reports whether k is a handler-internal item key that must not
// be emitted as a Firestore document field.
func isReservedKey(k string) bool {
	return k == fieldID || k == fieldCreateTime || k == fieldUpdateTime
}

// reference is a Go carrier for a Firestore referenceValue: a document
// resource path. A distinct type keeps it from round-tripping as a plain
// stringValue (which would lose the reference type through the SDK).
type reference string

// Handler serves Firestore REST API requests against a database driver.
type Handler struct {
	db dbdriver.Database
}

// New returns a Firestore handler backed by db.
func New(db dbdriver.Database) *Handler {
	return &Handler{db: db}
}

// Matches returns true for /v1/projects/.../databases/.../documents paths.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/v1/projects/")
}

// ServeHTTP routes the request based on URL path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Batched write API: POST .../documents:commit, .../documents:batchGet,
	// .../documents:runQuery — these end with `:action`.
	if action, base, ok := splitActionSuffix(r.URL.Path); ok {
		h.serveAction(w, r, base, action)
		return
	}

	parts, err := parseFirestorePath(r.URL.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	if parts.documentID == "" {
		// Collection-level operation.
		switch r.Method {
		case http.MethodPost:
			h.createDocument(w, r, parts)
		case http.MethodGet:
			h.listDocuments(w, r, parts)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getDocument(w, r, parts)
	case http.MethodPatch:
		h.updateDocument(w, r, parts)
	case http.MethodDelete:
		h.deleteDocument(w, r, parts)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

// splitActionSuffix detects URLs of the form "{base}:{action}" and returns
// the action name and base path. Example: "/v1/.../documents:commit" →
// ("commit", "/v1/.../documents", true).
func splitActionSuffix(path string) (action, base string, ok bool) {
	colonIdx := strings.LastIndex(path, ":")
	if colonIdx < 0 {
		return "", "", false
	}

	// Must be after the last "/" to be a method action; otherwise it's part
	// of the path (rare, but be safe).
	slashIdx := strings.LastIndex(path, "/")
	if colonIdx < slashIdx {
		return "", "", false
	}

	return path[colonIdx+1:], path[:colonIdx], true
}

// serveAction handles the batch write/read endpoints used by Firestore's
// REST API. Real Firestore's gRPC API uses individual RPCs; the REST API
// bundles them under :commit / :batchGet / :runQuery.
func (h *Handler) serveAction(w http.ResponseWriter, r *http.Request, base, action string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	switch action {
	case "commit":
		h.commit(w, r, base)
	case "batchGet":
		h.batchGet(w, r, base)
	case "runQuery":
		h.runQuery(w, r, base)
	case "beginTransaction":
		h.beginTransaction(w, r)
	case "rollback":
		h.rollback(w, r)
	case "listCollectionIds":
		h.listCollectionIDs(w, r)
	default:
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "action not implemented: "+action)
	}
}

// commitRequest mirrors the subset of Firestore's CommitRequest we accept.
type commitRequest struct {
	Writes []writeOp `json:"writes"`
}

type writeOp struct {
	Update          *document     `json:"update,omitempty"`
	Delete          string        `json:"delete,omitempty"`
	CurrentDocument *precondition `json:"currentDocument,omitempty"`
	UpdateMask      *struct {
		FieldPaths []string `json:"fieldPaths"`
	} `json:"updateMask,omitempty"`
}

// precondition mirrors google.firestore.v1.Precondition (exists only).
type precondition struct {
	Exists *bool `json:"exists,omitempty"`
}

type commitResponse struct {
	WriteResults []writeResult `json:"writeResults"`
	CommitTime   string        `json:"commitTime"`
}

type writeResult struct {
	UpdateTime string `json:"updateTime"`
}

// commit handles POST .../documents:commit — the batch-write endpoint the
// REST SDK uses for Set / Update / Delete. A `transaction` field, when present,
// is accepted and applied directly: the in-memory store has no isolation
// levels, so the writes commit exactly as a non-transactional batch would.
func (h *Handler) commit(w http.ResponseWriter, r *http.Request, _ string) {
	var req commitRequest

	if !decodeJSON(w, r, &req) {
		return
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	out := commitResponse{CommitTime: nowStr}

	for i := range req.Writes {
		op := &req.Writes[i]

		switch {
		case op.Update != nil:
			if !h.commitUpdate(w, r, op, now) {
				return
			}

			out.WriteResults = append(out.WriteResults, writeResult{UpdateTime: nowStr})
		case op.Delete != "":
			if !h.commitDelete(w, r, op.Delete) {
				return
			}

			out.WriteResults = append(out.WriteResults, writeResult{UpdateTime: nowStr})
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// commitUpdate applies one Update write. It returns false when it has already
// written an error response and the caller must stop.
func (h *Handler) commitUpdate(w http.ResponseWriter, r *http.Request, op *writeOp, now time.Time) bool {
	p, id, err := splitDocumentName(op.Update.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return false
	}

	existing, gerr := h.db.GetItem(r.Context(), p.collection, map[string]any{fieldID: id})
	if gerr != nil && !cerrors.IsNotFound(gerr) {
		writeErr(w, gerr)
		return false
	}

	exists := gerr == nil
	if !checkPrecondition(w, op.CurrentDocument, exists, op.Update.Name) {
		return false
	}

	item := fieldsToMap(op.Update.Fields)
	item[fieldID] = id

	// updateMask selects merge semantics: masked paths written (or deleted when
	// absent from the body), all other stored fields kept.
	if op.UpdateMask != nil && len(op.UpdateMask.FieldPaths) > 0 {
		item = mergeMasked(existing, item, id, op.UpdateMask.FieldPaths)
	}

	stampTimes(item, existing, now)

	h.ensureCollection(r.Context(), p.collection)

	if perr := h.db.PutItem(r.Context(), p.collection, item); perr != nil {
		writeErr(w, perr)
		return false
	}

	return true
}

// commitDelete applies one Delete write, returning false when an error
// response has already been written.
func (h *Handler) commitDelete(w http.ResponseWriter, r *http.Request, name string) bool {
	p, id, err := splitDocumentName(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return false
	}

	if derr := h.db.DeleteItem(r.Context(), p.collection, map[string]any{fieldID: id}); derr != nil {
		writeErr(w, derr)
		return false
	}

	return true
}

// checkPrecondition enforces a currentDocument.exists precondition, writing the
// matching Firestore error and returning false on violation.
func checkPrecondition(w http.ResponseWriter, pc *precondition, exists bool, name string) bool {
	if pc == nil || pc.Exists == nil {
		return true
	}

	if !*pc.Exists && exists {
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", "document already exists: "+name)
		return false
	}

	if *pc.Exists && !exists {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no document to update: "+name)
		return false
	}

	return true
}

// mergeMasked builds the merged item for a masked update: it starts from the
// stored document (preserving unmasked fields and the reserved keys), then for
// each masked path writes the new value or deletes it when absent from body.
func mergeMasked(existing, body map[string]any, id string, paths []string) map[string]any {
	merged := map[string]any{fieldID: id}
	for k, v := range existing {
		merged[k] = v
	}

	for _, path := range paths {
		if v, ok := body[path]; ok {
			merged[path] = v
		} else {
			delete(merged, path)
		}
	}

	return merged
}

// batchGet handles POST .../documents:batchGet — the batched-read endpoint.
type batchGetRequest struct {
	Documents []string `json:"documents"`
}

type batchGetResponseEntry struct {
	Found    *document `json:"found,omitempty"`
	Missing  string    `json:"missing,omitempty"`
	ReadTime string    `json:"readTime"`
}

func (h *Handler) batchGet(w http.ResponseWriter, r *http.Request, _ string) {
	var req batchGetRequest

	if !decodeJSON(w, r, &req) {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// REST batchGet returns ONE JSON array containing an entry per requested
	// document; emitting separate arrays would truncate the client's decode
	// to the first entry.
	entries := make([]batchGetResponseEntry, 0, len(req.Documents))

	for _, docName := range req.Documents {
		p, id, err := splitDocumentName(docName)
		if err != nil {
			entries = append(entries, batchGetResponseEntry{Missing: docName, ReadTime: now})
			continue
		}

		item, gerr := h.db.GetItem(r.Context(), p.collection, map[string]any{fieldID: id})
		if gerr != nil {
			entries = append(entries, batchGetResponseEntry{Missing: docName, ReadTime: now})
			continue
		}

		doc := mapToDocument(item, p, id)
		entries = append(entries, batchGetResponseEntry{Found: &doc, ReadTime: now})
	}

	writeJSON(w, http.StatusOK, entries)
}

// runQuery handles POST .../documents:runQuery — for collection scans.
// allResults asks the driver for the entire matched set (runQuery streams
// rather than pages; the driver's zero-limit default is 100).
const allResults = 1 << 30

type runQueryRequest struct {
	StructuredQuery structuredQuery `json:"structuredQuery"`
}

type structuredQuery struct {
	From []struct {
		CollectionID string `json:"collectionId"`
	} `json:"from"`
	Where   *queryFilter    `json:"where"`
	OrderBy []orderByClause `json:"orderBy"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
	StartAt *queryCursor    `json:"startAt"`
	EndAt   *queryCursor    `json:"endAt"`
	Select  *selectClause   `json:"select"`
}

type orderByClause struct {
	Field     fieldRef  `json:"field"`
	Direction direction `json:"direction"` // ASCENDING (default) or DESCENDING
}

// direction decodes an ORDER BY direction sent as its enum name ("DESCENDING")
// or its protobuf number (ASCENDING=1, DESCENDING=2).
type direction string

//nolint:gochecknoglobals // static lookup table
var directionNames = map[int]direction{1: "ASCENDING", 2: "DESCENDING"}

func (d *direction) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*d = direction(v)
		return nil
	}

	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*d = directionNames[n]
	return nil
}

// queryCursor is a startAt/endAt boundary: values align positionally to the
// orderBy fields, and before selects the inclusive/exclusive side.
type queryCursor struct {
	Values []value `json:"values"`
	Before bool    `json:"before"`
}

type selectClause struct {
	Fields []fieldRef `json:"fields"`
}

// queryFilter mirrors StructuredQuery.Filter: a single fieldFilter, a
// compositeFilter (AND/OR) of nested filters, or a unaryFilter.
type queryFilter struct {
	FieldFilter     *fieldFilter     `json:"fieldFilter"`
	CompositeFilter *compositeFilter `json:"compositeFilter"`
	UnaryFilter     *unaryFilter     `json:"unaryFilter"`
}

type fieldRef struct {
	FieldPath string `json:"fieldPath"`
}

type fieldFilter struct {
	Field fieldRef `json:"field"`
	Op    fieldOp  `json:"op"`
	Value value    `json:"value"`
}

type compositeFilter struct {
	Op      compositeOp   `json:"op"`
	Filters []queryFilter `json:"filters"`
}

type unaryFilter struct {
	Op    unaryOp  `json:"op"`
	Field fieldRef `json:"field"`
}

// fieldOp decodes a FieldFilter operator sent either as its enum name
// ("EQUAL") or its protobuf number (5), as the REST client does.
type fieldOp string

//nolint:gochecknoglobals // static lookup table
var fieldOpNames = map[int]fieldOp{
	1:  "LESS_THAN",
	2:  "LESS_THAN_OR_EQUAL",
	3:  "GREATER_THAN",
	4:  "GREATER_THAN_OR_EQUAL",
	5:  "EQUAL",
	6:  "NOT_EQUAL",
	7:  "ARRAY_CONTAINS",
	8:  "IN",
	9:  "ARRAY_CONTAINS_ANY",
	10: "NOT_IN",
}

func (o *fieldOp) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*o = fieldOp(v)
		return nil
	}

	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*o = fieldOpNames[n] // unknown numbers stay "" and fail downstream
	return nil
}

// compositeOp decodes a CompositeFilter operator name or number (AND=1, OR=2).
type compositeOp string

const (
	compositeAnd compositeOp = "AND"
	compositeOr  compositeOp = "OR"
)

//nolint:gochecknoglobals // static lookup table
var compositeOpNames = map[int]compositeOp{1: compositeAnd, 2: compositeOr}

func (o *compositeOp) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*o = compositeOp(v)
		return nil
	}

	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*o = compositeOpNames[n] // unknown numbers stay "" and fail downstream
	return nil
}

// unaryOp decodes a UnaryFilter operator name or number (IS_NAN=2, IS_NULL=3,
// IS_NOT_NAN=4, IS_NOT_NULL=5).
type unaryOp string

//nolint:gochecknoglobals // static lookup table
var unaryOpNames = map[int]unaryOp{2: "IS_NAN", 3: "IS_NULL", 4: "IS_NOT_NAN", 5: "IS_NOT_NULL"}

func (o *unaryOp) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*o = unaryOp(v)
		return nil
	}

	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*o = unaryOpNames[n]
	return nil
}

type runQueryResponseEntry struct {
	Document *document `json:"document,omitempty"`
	ReadTime string    `json:"readTime"`
}

func (h *Handler) runQuery(w http.ResponseWriter, r *http.Request, base string) {
	var req runQueryRequest

	if !decodeJSON(w, r, &req) {
		return
	}

	if len(req.StructuredQuery.From) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "from clause required")
		return
	}

	collection := req.StructuredQuery.From[0].CollectionID

	// Project + database from the base path.
	p, _ := parseFirestorePath(base)
	p.collection = collection

	node, ferr := buildFilterNode(req.StructuredQuery.Where)
	if ferr != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", ferr.Error())
		return
	}

	// Fetch the full collection and evaluate the where clause here with full
	// grammar fidelity (type-aware, AND/OR/NOT, IN/array-contains, unary).
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: collection, Limit: allResults})
	if err != nil {
		writeErr(w, err)
		return
	}

	matched, merr := filterDocuments(result.Items, node)
	if merr != nil {
		writeErr(w, merr)
		return
	}

	// Shape the matched set: order by, cursors, offset/limit, then select.
	matched = shapeResults(matched, &req.StructuredQuery)

	streamQueryResults(w, matched, p)
}

// filterDocuments keeps the items matching node (a nil node matches all).
func filterDocuments(items []map[string]any, node expr.Node) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(items))

	for _, item := range items {
		if node == nil {
			out = append(out, item)
			continue
		}

		ok, err := expr.Eval(node, item)
		if err != nil {
			return nil, err
		}

		if ok {
			out = append(out, item)
		}
	}

	return out, nil
}

func streamQueryResults(w http.ResponseWriter, items []map[string]any, p firestorePath) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("[")) //nolint:errcheck // best-effort streaming response

	for i, item := range items {
		if i > 0 {
			w.Write([]byte(",")) //nolint:errcheck // best-effort
		}

		id, _ := item[fieldID].(string)
		doc := mapToDocument(item, p, id)

		_ = json.NewEncoder(w).Encode(runQueryResponseEntry{Document: &doc, ReadTime: now})
	}

	w.Write([]byte("]")) //nolint:errcheck // best-effort
}

// splitDocumentName parses "projects/{p}/databases/{db}/documents/{coll}/{id}"
// into a firestorePath plus the document id.
func splitDocumentName(name string) (firestorePath, string, error) {
	parts := strings.Split(name, "/")

	const (
		minParts                = 6
		idxProject, idxDatabase = 1, 3
		idxCollection           = 5
		idxID                   = 6
	)

	if len(parts) < minParts ||
		parts[0] != segProjects ||
		parts[2] != segDatabases ||
		parts[4] != segDocuments {
		return firestorePath{}, "", fmt.Errorf("%w: %s", errInvalidDocName, name)
	}

	p := firestorePath{
		project:    parts[idxProject],
		database:   parts[idxDatabase],
		collection: parts[idxCollection],
	}

	if len(parts) <= idxID {
		return p, "", fmt.Errorf("%w: %s", errMissingDocID, name)
	}

	return p, strings.Join(parts[idxID:], "/"), nil
}

// firestorePath holds the components extracted from a Firestore URL.
type firestorePath struct {
	project    string
	database   string
	collection string
	documentID string
}

// parseFirestorePath extracts the project, database, collection, and
// optional document id from a Firestore REST path.
//
// /v1/projects/{p}/databases/{db}/documents/{collection}
// /v1/projects/{p}/databases/{db}/documents/{collection}/{id}.
func parseFirestorePath(path string) (firestorePath, error) {
	rest := strings.TrimPrefix(path, "/v1/")

	parts := strings.Split(rest, "/")

	const (
		minParts      = 6 // projects/{p}/databases/{db}/documents/{collection}
		fullDocParts  = 7 // ... + /{id}
		idxProject    = 1
		idxDatabase   = 3
		idxCollection = 5
		idxDocument   = 6
	)

	if len(parts) < minParts ||
		parts[0] != segProjects ||
		parts[2] != segDatabases ||
		parts[4] != segDocuments {
		return firestorePath{}, errNotDocPath
	}

	out := firestorePath{
		project:    parts[idxProject],
		database:   parts[idxDatabase],
		collection: parts[idxCollection],
	}

	if len(parts) >= fullDocParts {
		out.documentID = strings.Join(parts[idxDocument:], "/")
	}

	return out, nil
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	docID := r.URL.Query().Get("documentId")

	explicitID := docID != ""
	if !explicitID {
		// Auto-generate an ID; Firestore's default IDs are 20-char IDs but
		// any string is fine for our purposes.
		docID = "auto-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	var inDoc document

	if !decodeJSON(w, r, &inDoc) {
		return
	}

	// CreateDocument with an explicit id must fail if that id already exists,
	// rather than silently overwriting (real Firestore returns ALREADY_EXISTS).
	if explicitID {
		if _, err := h.db.GetItem(r.Context(), p.collection, map[string]any{fieldID: docID}); err == nil {
			writeError(w, http.StatusConflict, "ALREADY_EXISTS",
				"document "+docID+" already exists")

			return
		}
	}

	item := fieldsToMap(inDoc.Fields)
	item[fieldID] = docID
	stampTimes(item, nil, time.Now().UTC())

	// Firestore creates a collection lazily on first write; the driver requires
	// the "table" to exist, so ensure it before writing.
	h.ensureCollection(r.Context(), p.collection)

	if err := h.db.PutItem(r.Context(), p.collection, item); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mapToDocument(item, p, docID))
}

// ensureCollection lazily creates a Firestore collection (driver table keyed on
// the document "id") so a first write doesn't fail with "collection not found".
// An already-exists result is benign.
func (h *Handler) ensureCollection(ctx context.Context, collection string) {
	_ = h.db.CreateTable(ctx, dbdriver.TableConfig{Name: collection, PartitionKey: "id"})
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	item, err := h.db.GetItem(r.Context(), p.collection, map[string]any{fieldID: p.documentID})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mapToDocument(item, p, p.documentID))
}

// listDocuments handles GET .../{collection} — the ListDocuments RPC. It honors
// pageSize/pageToken (so a large collection pages fully instead of silently
// truncating at the driver's default limit), the optional orderBy, and
// mask.fieldPaths field projection.
func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request, p firestorePath) {
	// Fetch the whole collection, then order + page in the handler so orderBy
	// stays correct across page boundaries.
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: p.collection, Limit: allResults})
	if err != nil {
		writeErr(w, err)
		return
	}

	q := r.URL.Query()

	items := result.Items
	applyListOrderBy(items, q.Get("orderBy"))

	page, perr := pagination.Paginate(items, q.Get("pageToken"), atoiOr(q.Get("pageSize"), 0))
	if perr != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pageToken")
		return
	}

	mask := q["mask.fieldPaths"]

	out := listDocumentsResponse{NextPageToken: page.NextPageToken}

	for _, item := range page.Items {
		id, _ := item[fieldID].(string)
		doc := mapToDocument(item, p, id)
		applyFieldMask(&doc, mask)
		out.Documents = append(out.Documents, doc)
	}

	writeJSON(w, http.StatusOK, out)
}

// applyListOrderBy sorts items by a ListDocuments orderBy clause: a
// comma-separated list of field paths, each optionally suffixed " desc". An
// empty clause keeps the driver's default document-id order.
func applyListOrderBy(items []map[string]any, orderBy string) {
	orderBy = strings.TrimSpace(orderBy)
	if orderBy == "" {
		return
	}

	keys := parseOrderBy(orderBy)

	sort.SliceStable(items, func(i, j int) bool {
		return compareByKeys(items[i], items[j], keys) < 0
	})
}

// parseOrderBy turns "score desc, name" into resolved sort keys.
func parseOrderBy(orderBy string) []sortKey {
	terms := strings.Split(orderBy, ",")
	keys := make([]sortKey, 0, len(terms))

	for _, term := range terms {
		fields := strings.Fields(term)
		if len(fields) == 0 {
			continue
		}

		desc := len(fields) > 1 && strings.EqualFold(fields[1], "desc")
		keys = append(keys, sortKey{path: fields[0], desc: desc})
	}

	return keys
}

// applyFieldMask trims a document's fields to the requested mask paths. An
// empty mask leaves the document unchanged (all fields returned).
func applyFieldMask(doc *document, mask []string) {
	if len(mask) == 0 || doc.Fields == nil {
		return
	}

	kept := make(map[string]value, len(mask))

	for _, path := range mask {
		if v, ok := doc.Fields[path]; ok {
			kept[path] = v
		}
	}

	doc.Fields = kept
}

// atoiOr parses s as an int, returning def when s is empty or invalid.
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}

	if n, err := strconv.Atoi(s); err == nil {
		return n
	}

	return def
}

func (h *Handler) updateDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	var inDoc document

	if !decodeJSON(w, r, &inDoc) {
		return
	}

	existing, err := h.db.GetItem(r.Context(), p.collection, map[string]any{fieldID: p.documentID})
	if err != nil && !cerrors.IsNotFound(err) {
		writeErr(w, err)
		return
	}

	item := fieldsToMap(inDoc.Fields)
	item[fieldID] = p.documentID

	// With an updateMask, real Firestore merges: only the masked field paths are
	// written; every other stored field is preserved. A masked path absent from
	// the body is a field delete. Without a mask, the document is replaced
	// wholesale. createTime is preserved from the existing document either way.
	if mask := r.URL.Query()["updateMask.fieldPaths"]; len(mask) > 0 {
		item = mergeMasked(existing, item, p.documentID, mask)
	}

	stampTimes(item, existing, time.Now().UTC())

	if err := h.db.PutItem(r.Context(), p.collection, item); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mapToDocument(item, p, p.documentID))
}

func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	if err := h.db.DeleteItem(r.Context(), p.collection, map[string]any{fieldID: p.documentID}); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// mapToDocument converts a driver-shaped item map into a Firestore document.
// createTime/updateTime are read from the stored item so repeated reads of an
// unchanged document return stable commit timestamps (real Firestore behavior),
// rather than being regenerated on every read.
func mapToDocument(item map[string]any, p firestorePath, id string) document {
	return document{
		Name: fmt.Sprintf("projects/%s/databases/%s/documents/%s/%s",
			p.project, p.database, p.collection, id),
		Fields:     mapToFields(item),
		CreateTime: storedTime(item, fieldCreateTime),
		UpdateTime: storedTime(item, fieldUpdateTime),
	}
}

// storedTime formats a reserved timestamp key as an RFC3339 string. Legacy
// items written before timestamps were tracked fall back to the current time.
func storedTime(item map[string]any, key string) string {
	if t, ok := item[key].(time.Time); ok {
		return t.UTC().Format(time.RFC3339Nano)
	}

	return time.Now().UTC().Format(time.RFC3339Nano)
}

// stampTimes records commit timestamps on item before it is written. On a
// first write (no existing document) createTime and updateTime are both now;
// on an overwrite/update createTime is preserved from the existing document so
// it stays stable while updateTime advances.
func stampTimes(item, existing map[string]any, now time.Time) {
	if ct, ok := existing[fieldCreateTime].(time.Time); ok {
		item[fieldCreateTime] = ct
	} else {
		item[fieldCreateTime] = now
	}

	item[fieldUpdateTime] = now
}

// mapToFields converts a driver item map to typed Firestore field values,
// excluding the synthetic id field that we use as a primary key.
func mapToFields(item map[string]any) map[string]value {
	if len(item) == 0 {
		return nil
	}

	fields := make(map[string]value, len(item))

	for k, v := range item {
		if isReservedKey(k) {
			continue
		}

		fields[k] = goValueToFirestore(v)
	}

	if len(fields) == 0 {
		return nil
	}

	return fields
}

// fieldsToMap converts Firestore typed field values back into a Go map.
func fieldsToMap(fields map[string]value) map[string]any {
	out := make(map[string]any, len(fields))

	for k, v := range fields {
		out[k] = firestoreValueToGo(v)
	}

	return out
}

// goValueToFirestore picks the correct typed wrapper for a Go value.
func goValueToFirestore(v any) value {
	if tv, ok := goTypedToFirestore(v); ok {
		return tv
	}

	switch x := v.(type) {
	case string:
		return value{StringValue: &x}
	case bool:
		return value{BooleanValue: &x}
	case int, int32, int64:
		return goIntToFirestore(x)
	case float64:
		// A float always re-encodes as doubleValue so an integer-valued double
		// (30.0) round-trips as float64, not int64.
		return value{DoubleValue: &x}
	case []any:
		return goArrayToFirestore(x)
	case map[string]any:
		return goMapToFirestore(x)
	case nil:
		nullStr := "NULL_VALUE"

		return value{NullValue: &nullStr}
	default:
		s := fmt.Sprintf("%v", x)

		return value{StringValue: &s}
	}
}

// goTypedToFirestore encodes the non-primitive Firestore value types
// (timestamp, bytes, reference, geo point) whose Go carriers must round-trip
// without decaying to a string. It reports false for anything it does not own.
func goTypedToFirestore(v any) (value, bool) {
	switch x := v.(type) {
	case time.Time:
		s := x.UTC().Format(time.RFC3339Nano)

		return value{TimestampValue: &s}, true
	case []byte:
		s := base64.StdEncoding.EncodeToString(x)

		return value{BytesValue: &s}, true
	case reference:
		s := string(x)

		return value{ReferenceValue: &s}, true
	case *geoPoint:
		return value{GeoPointValue: x}, true
	case geoPoint:
		gp := x

		return value{GeoPointValue: &gp}, true
	default:
		return value{}, false
	}
}

func goIntToFirestore(x any) value {
	var n int64

	switch v := x.(type) {
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	}

	s := strconv.FormatInt(n, 10)

	return value{IntegerValue: &s}
}

func goArrayToFirestore(x []any) value {
	arr := arrayValue{Values: make([]value, len(x))}

	for i, el := range x {
		arr.Values[i] = goValueToFirestore(el)
	}

	return value{ArrayValue: &arr}
}

func goMapToFirestore(x map[string]any) value {
	m := mapValue{Fields: make(map[string]value, len(x))}

	for k, mv := range x {
		m.Fields[k] = goValueToFirestore(mv)
	}

	return value{MapValue: &m}
}

//nolint:gocritic // v is by-design a value type for the field unmarshaller
func firestoreValueToGo(v value) any {
	if x := firestoreScalarToGo(v); x != skipScalar {
		return x
	}

	switch {
	case v.ArrayValue != nil:
		out := make([]any, len(v.ArrayValue.Values))
		for i, el := range v.ArrayValue.Values {
			out[i] = firestoreValueToGo(el)
		}

		return out
	case v.MapValue != nil:
		out := make(map[string]any, len(v.MapValue.Fields))
		for k, mv := range v.MapValue.Fields {
			out[k] = firestoreValueToGo(mv)
		}

		return out
	}

	return nil
}

// skipScalar is a sentinel returned by firestoreScalarToGo to mean: this
// value is not a scalar, try the composite branches.
//
//nolint:gochecknoglobals // sentinel value
var skipScalar = struct{}{}

//nolint:gocritic // v is by-design a value type for the field unmarshaller
func firestoreScalarToGo(v value) any {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.BooleanValue != nil:
		return *v.BooleanValue
	case v.IntegerValue != nil:
		if n, err := strconv.ParseInt(*v.IntegerValue, 10, 64); err == nil {
			return n
		}

		return *v.IntegerValue
	case v.DoubleValue != nil:
		return *v.DoubleValue
	case v.NullValue != nil:
		return nil
	}

	if x, ok := firestoreTypedScalarToGo(v); ok {
		return x
	}

	return skipScalar
}

// firestoreTypedScalarToGo decodes the non-primitive scalar value types
// (timestamp, bytes, reference, geo point) into Go carriers that re-encode
// losslessly. It reports false when v holds none of them.
//
//nolint:gocritic // v is by-design a value type for the field unmarshaller
func firestoreTypedScalarToGo(v value) (any, bool) {
	switch {
	case v.TimestampValue != nil:
		if t, err := time.Parse(time.RFC3339Nano, *v.TimestampValue); err == nil {
			return t, true
		}

		return *v.TimestampValue, true
	case v.BytesValue != nil:
		if b, err := base64.StdEncoding.DecodeString(*v.BytesValue); err == nil {
			return b, true
		}

		return *v.BytesValue, true
	case v.ReferenceValue != nil:
		return reference(*v.ReferenceValue), true
	case v.GeoPointValue != nil:
		return v.GeoPointValue, true
	default:
		return nil, false
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, statusCode, msg string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    status,
			Message: msg,
			Status:  statusCode,
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
