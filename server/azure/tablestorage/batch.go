package tablestorage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/tablestorage/driver"
)

// maxBatchBytes caps a whole entity-group-transaction payload (Azure allows 4 MiB).
const maxBatchBytes = 4 << 20

// maxBatchOps is the Table service limit of operations per change set.
const maxBatchOps = 100

// methodMerge is the OData MERGE verb used by change-set merge operations.
const methodMerge = "MERGE"

func batchErr(msg string) error {
	return cerrors.New(cerrors.InvalidArgument, msg)
}

// batch handles POST /$batch — an OData entity group transaction. It parses the
// multipart/mixed batch + change set, applies the operations atomically, and
// returns the multipart/mixed batch response the aztables client expects.
func (h *Handler) batch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	ops, table, err := parseBatch(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
		return
	}

	if len(ops) == 0 {
		writeError(w, http.StatusBadRequest, "InvalidInput", "empty change set")
		return
	}

	if len(ops) > maxBatchOps {
		writeError(w, http.StatusBadRequest, "InvalidInput", "change set exceeds 100 operations")
		return
	}

	if code, msg, ok := validateBatchPartitions(ops); !ok {
		writeError(w, http.StatusBadRequest, code, msg)
		return
	}

	results, applyErr := h.ts.ApplyBatch(r.Context(), table, ops)
	if applyErr != nil {
		writeBatchFailure(w, applyErr)
		return
	}

	writeBatchSuccess(w, ops, results)
}

// validateBatchPartitions enforces Azure's entity-group-transaction rules: every
// operation must target the same PartitionKey, and an entity — identified by its
// (PartitionKey, RowKey) — may appear at most once. It returns the Azure error
// code and message when a rule is violated. ops must be non-empty.
func validateBatchPartitions(ops []driver.BatchOp) (code, msg string, ok bool) {
	partition := ops[0].PartitionKey
	seen := make(map[string]struct{}, len(ops))

	for _, op := range ops {
		if op.PartitionKey != partition {
			return "CommandsInBatchActOnDifferentPartitions",
				"all commands in a batch transaction must operate on the same partition key", false
		}

		key := op.PartitionKey + "\x00" + op.RowKey
		if _, dup := seen[key]; dup {
			return "InvalidDuplicateRow",
				"an entity can appear only once in a batch transaction", false
		}

		seen[key] = struct{}{}
	}

	return "", "", true
}

// parseBatch decodes the multipart batch body into an ordered op list and the
// (single) target table name.
func parseBatch(w http.ResponseWriter, r *http.Request) ([]driver.BatchOp, string, error) {
	changeset, err := changesetReader(w, r)
	if err != nil {
		return nil, "", err
	}

	var (
		ops   []driver.BatchOp
		table string
	)

	for {
		part, perr := changeset.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}

		if perr != nil {
			return nil, "", fmt.Errorf("malformed change set: %w", perr)
		}

		raw, rerr := io.ReadAll(part)
		if rerr != nil {
			return nil, "", rerr
		}

		op, opTable, oerr := parseInnerRequest(raw)
		if oerr != nil {
			return nil, "", oerr
		}

		if table == "" {
			table = opTable
		}

		ops = append(ops, op)
	}

	return ops, table, nil
}

// changesetReader unwraps the outer batch part and returns a reader over the
// inner change set's operations.
func changesetReader(w http.ResponseWriter, r *http.Request) (*multipart.Reader, error) {
	boundary, err := boundaryOf(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}

	body := http.MaxBytesReader(w, r.Body, maxBatchBytes)
	outer := multipart.NewReader(body, boundary)

	part, err := outer.NextPart()
	if err != nil {
		return nil, fmt.Errorf("empty batch: %w", err)
	}

	inner, err := boundaryOf(part.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}

	return multipart.NewReader(part, inner), nil
}

func boundaryOf(contentType string) (string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("invalid multipart content type: %w", err)
	}

	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		return "", batchErr("missing multipart boundary")
	}

	return boundary, nil
}

// parseInnerRequest turns one application/http change-set part into a BatchOp.
// The multipart reader has already stripped the part's MIME headers, so raw is
// the embedded HTTP request ("POST … HTTP/1.1\r\n<headers>\r\n\r\n<body>").
func parseInnerRequest(raw []byte) (driver.BatchOp, string, error) {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return driver.BatchOp{}, "", fmt.Errorf("malformed inner request: %w", err)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return driver.BatchOp{}, "", err
	}

	path := strings.TrimPrefix(req.URL.Path, "/")

	if !strings.ContainsRune(path, '(') {
		return insertOp(path, bodyBytes)
	}

	table, predicate, ok := splitEntityPath(path)
	if !ok {
		return driver.BatchOp{}, "", batchErr("malformed entity path in change set")
	}

	pk, rk, ok := parseKeyPredicate(predicate)
	if !ok {
		return driver.BatchOp{}, "", batchErr("malformed key predicate in change set")
	}

	op, err := entityOpFromRequest(req, pk, rk, bodyBytes)
	if err != nil {
		return driver.BatchOp{}, "", err
	}

	return op, table, nil
}

// insertOp builds an insert op from a bare "POST /{table}" change-set request.
func insertOp(table string, body []byte) (driver.BatchOp, string, error) {
	ent, err := decodeEntity(body)
	if err != nil {
		return driver.BatchOp{}, "", err
	}

	return driver.BatchOp{
		Type:         driver.BatchInsert,
		PartitionKey: asString(ent["PartitionKey"]),
		RowKey:       asString(ent["RowKey"]),
		Entity:       ent,
	}, table, nil
}

// entityOpFromRequest maps a keyed change-set request (PUT/MERGE/PATCH/DELETE)
// to a BatchOp, distinguishing upsert (no If-Match) from update (If-Match set).
func entityOpFromRequest(req *http.Request, pk, rk string, body []byte) (driver.BatchOp, error) {
	ifMatch := req.Header.Get("If-Match")

	op := driver.BatchOp{PartitionKey: pk, RowKey: rk, IfMatch: ifMatch}

	if req.Method == http.MethodDelete {
		op.Type = driver.BatchDelete
		return op, nil
	}

	ent, err := decodeEntity(body)
	if err != nil {
		return driver.BatchOp{}, err
	}

	op.Entity = ent

	switch req.Method {
	case http.MethodPut:
		op.Type = pick(ifMatch, driver.BatchUpdateReplace, driver.BatchUpsertReplace)
	case http.MethodPatch, methodMerge:
		op.Type = pick(ifMatch, driver.BatchUpdateMerge, driver.BatchUpsertMerge)
	default:
		return driver.BatchOp{}, cerrors.Newf(cerrors.InvalidArgument, "unsupported change-set method %q", req.Method)
	}

	return op, nil
}

// pick returns whenSet when an If-Match precondition is present, else whenUnset.
func pick(ifMatch string, whenSet, whenUnset driver.BatchOpType) driver.BatchOpType {
	if ifMatch != "" {
		return whenSet
	}

	return whenUnset
}

func decodeEntity(body []byte) (driver.Entity, error) {
	var ent driver.Entity
	if err := json.Unmarshal(body, &ent); err != nil {
		return nil, fmt.Errorf("malformed entity in change set: %w", err)
	}

	return ent, nil
}

// changeSet is a rendered change-set multipart document plus its boundary.
type changeSet struct {
	body     []byte
	boundary string
}

// writeBatchSuccess emits a 202 batch response whose single change set reports
// each op's outcome (204 No Content, with the new ETag for writes).
func writeBatchSuccess(w http.ResponseWriter, ops []driver.BatchOp, results []driver.BatchResult) {
	cs := buildChangeset(func(cw *multipart.Writer) {
		for i := range ops {
			writeOpPart(cw, opSuccessResponse(i, ops[i], results[i]))
		}
	})

	writeBatchEnvelope(w, cs)
}

// writeBatchFailure emits a 202 batch response whose change set carries the
// single failed op as a 4xx sub-response (atomic: nothing was applied).
func writeBatchFailure(w http.ResponseWriter, err error) {
	idx := 0

	var be *driver.BatchError
	if errors.As(err, &be) {
		idx = be.Index
	}

	status, code := mapErr(err)

	cs := buildChangeset(func(cw *multipart.Writer) {
		writeOpPart(cw, opErrorResponse(idx, status, code, err.Error()))
	})

	writeBatchEnvelope(w, cs)
}

// buildChangeset renders a change-set multipart document via fn.
func buildChangeset(fn func(*multipart.Writer)) changeSet {
	var buf bytes.Buffer

	cw := multipart.NewWriter(&buf)
	fn(cw)
	_ = cw.Close()

	return changeSet{body: buf.Bytes(), boundary: cw.Boundary()}
}

// writeOpPart writes one application/http sub-response into the change set.
func writeOpPart(cw *multipart.Writer, payload string) {
	hdr := textproto.MIMEHeader{
		"Content-Type":              {"application/http"},
		"Content-Transfer-Encoding": {"binary"},
	}

	part, err := cw.CreatePart(hdr)
	if err != nil {
		return
	}

	_, _ = io.WriteString(part, payload)
}

// writeBatchEnvelope wraps the change set in the outer batch multipart body and
// writes the 202 response.
func writeBatchEnvelope(w http.ResponseWriter, cs changeSet) {
	var buf bytes.Buffer

	bw := multipart.NewWriter(&buf)

	part, err := bw.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"multipart/mixed; boundary=" + cs.boundary},
	})
	if err == nil {
		_, _ = part.Write(cs.body)
	}

	_ = bw.Close()

	w.Header().Set("Content-Type", "multipart/mixed; boundary="+bw.Boundary())
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(buf.Bytes())
}

// opSuccessResponse renders the embedded HTTP response for a successful op.
func opSuccessResponse(idx int, op driver.BatchOp, res driver.BatchResult) string {
	var b strings.Builder

	b.WriteString("HTTP/1.1 204 No Content\r\n")
	fmt.Fprintf(&b, "Content-ID: %d\r\n", idx+1)
	b.WriteString("X-Content-Type-Options: nosniff\r\n")
	b.WriteString("Cache-Control: no-cache\r\n")
	b.WriteString("DataServiceVersion: 3.0;\r\n")

	if op.Type != driver.BatchDelete && res.ETag != "" {
		fmt.Fprintf(&b, "ETag: %s\r\n", res.ETag)
	}

	b.WriteString("\r\n")

	return b.String()
}

// opErrorResponse renders the embedded HTTP response for the failed op. The
// error message is prefixed with the op index, as the real service does.
func opErrorResponse(idx, status int, code, msg string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	fmt.Fprintf(&b, "Content-ID: %d\r\n", idx+1)
	b.WriteString("X-Content-Type-Options: nosniff\r\n")
	b.WriteString("Content-Type: application/json;odata=minimalmetadata;streaming=true;charset=utf-8\r\n")
	b.WriteString("\r\n")

	body := map[string]any{
		"odata.error": map[string]any{
			"code": code,
			"message": map[string]any{
				"lang":  "en-US",
				"value": fmt.Sprintf("%d:%s", idx, msg),
			},
		},
	}

	encoded, _ := json.Marshal(body)
	b.Write(encoded)

	return b.String()
}
