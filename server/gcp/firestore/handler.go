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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbdriver "github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
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
	default:
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "action not implemented: "+action)
	}
}

// commitRequest mirrors the subset of Firestore's CommitRequest we accept.
type commitRequest struct {
	Writes []writeOp `json:"writes"`
}

type writeOp struct {
	Update *document `json:"update,omitempty"`
	Delete string    `json:"delete,omitempty"`
}

type commitResponse struct {
	WriteResults []writeResult `json:"writeResults"`
	CommitTime   string        `json:"commitTime"`
}

type writeResult struct {
	UpdateTime string `json:"updateTime"`
}

// commit handles POST .../documents:commit — the batch-write endpoint the
// REST SDK uses for Set / Update / Delete.
func (h *Handler) commit(w http.ResponseWriter, r *http.Request, _ string) {
	var req commitRequest

	if !decodeJSON(w, r, &req) {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	out := commitResponse{CommitTime: now}

	for _, op := range req.Writes {
		switch {
		case op.Update != nil:
			p, id, err := splitDocumentName(op.Update.Name)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}

			item := fieldsToMap(op.Update.Fields)
			item["id"] = id

			if perr := h.db.PutItem(r.Context(), p.collection, item); perr != nil {
				writeErr(w, perr)
				return
			}

			out.WriteResults = append(out.WriteResults, writeResult{UpdateTime: now})
		case op.Delete != "":
			p, id, err := splitDocumentName(op.Delete)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}

			if derr := h.db.DeleteItem(r.Context(), p.collection, map[string]any{"id": id}); derr != nil {
				writeErr(w, derr)
				return
			}

			out.WriteResults = append(out.WriteResults, writeResult{UpdateTime: now})
		}
	}

	writeJSON(w, http.StatusOK, out)
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

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)

	// REST batchGet returns a JSON array of response entries (newline-
	// delimited objects in the streaming HTTP response).
	for _, docName := range req.Documents {
		p, id, err := splitDocumentName(docName)
		if err != nil {
			_ = enc.Encode([]batchGetResponseEntry{{Missing: docName, ReadTime: now}})
			continue
		}

		item, gerr := h.db.GetItem(r.Context(), p.collection, map[string]any{"id": id})
		if gerr != nil {
			_ = enc.Encode([]batchGetResponseEntry{{Missing: docName, ReadTime: now}})
			continue
		}

		doc := mapToDocument(item, p, id)
		_ = enc.Encode([]batchGetResponseEntry{{Found: &doc, ReadTime: now}})
	}
}

// runQuery handles POST .../documents:runQuery — for collection scans.
type runQueryRequest struct {
	StructuredQuery struct {
		From []struct {
			CollectionID string `json:"collectionId"`
		} `json:"from"`
	} `json:"structuredQuery"`
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

	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: collection})
	if err != nil {
		writeErr(w, err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Stream JSON array of entries.
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("[")) //nolint:errcheck // best-effort streaming response

	for i, item := range result.Items {
		if i > 0 {
			w.Write([]byte(",")) //nolint:errcheck // best-effort
		}

		id, _ := item["id"].(string)
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
	if docID == "" {
		// Auto-generate an ID; Firestore's default IDs are 20-char IDs but
		// any string is fine for our purposes.
		docID = "auto-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	var inDoc document

	if !decodeJSON(w, r, &inDoc) {
		return
	}

	item := fieldsToMap(inDoc.Fields)
	item["id"] = docID

	if err := h.db.PutItem(r.Context(), p.collection, item); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mapToDocument(item, p, docID))
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	item, err := h.db.GetItem(r.Context(), p.collection, map[string]any{"id": p.documentID})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mapToDocument(item, p, p.documentID))
}

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request, p firestorePath) {
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: p.collection})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listDocumentsResponse{}

	for _, item := range result.Items {
		id, _ := item["id"].(string)
		out.Documents = append(out.Documents, mapToDocument(item, p, id))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) updateDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	var inDoc document

	if !decodeJSON(w, r, &inDoc) {
		return
	}

	item := fieldsToMap(inDoc.Fields)
	item["id"] = p.documentID

	if err := h.db.PutItem(r.Context(), p.collection, item); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mapToDocument(item, p, p.documentID))
}

func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request, p firestorePath) {
	if err := h.db.DeleteItem(r.Context(), p.collection, map[string]any{"id": p.documentID}); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// mapToDocument converts a driver-shaped item map into a Firestore document.
func mapToDocument(item map[string]any, p firestorePath, id string) document {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	return document{
		Name: fmt.Sprintf("projects/%s/databases/%s/documents/%s/%s",
			p.project, p.database, p.collection, id),
		Fields:     mapToFields(item),
		CreateTime: now,
		UpdateTime: now,
	}
}

// mapToFields converts a driver item map to typed Firestore field values,
// excluding the synthetic id field that we use as a primary key.
func mapToFields(item map[string]any) map[string]value {
	if len(item) == 0 {
		return nil
	}

	fields := make(map[string]value, len(item))

	for k, v := range item {
		if k == "id" {
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
	switch x := v.(type) {
	case string:
		return value{StringValue: &x}
	case bool:
		return value{BooleanValue: &x}
	case int, int32, int64:
		return goIntToFirestore(x)
	case float64:
		return goFloat64ToFirestore(x)
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

// goFloat64ToFirestore encodes integer-valued floats as IntegerValue so
// reads round-trip with the same Go type the SDK expects.
func goFloat64ToFirestore(x float64) value {
	if x == float64(int64(x)) {
		s := strconv.FormatInt(int64(x), 10)
		return value{IntegerValue: &s}
	}

	return value{DoubleValue: &x}
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
	case v.TimestampValue != nil:
		return *v.TimestampValue
	}

	return skipScalar
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
