package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// DynamoDB request-size limits enforced by the wire layer, matching real AWS.
const (
	maxBatchWriteItems = 25  // BatchWriteItem: total put/delete requests per call
	maxBatchGetItems   = 100 // BatchGetItem: total keys per call
)

// updateItem handles UpdateItem. Supports the common cases:
//   - UpdateExpression with "SET attr = :val" and "REMOVE attr"
//   - ExpressionAttributeValues for :val placeholders
//   - ExpressionAttributeNames for #name placeholders
func (h *Handler) updateItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		UpdateExpression          string            `json:"UpdateExpression"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ReturnValues              string            `json:"ReturnValues"`
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key := fromWireItem(req.Key)
	vals := fromWireItem(req.ExpressionAttributeValues)

	// ExpressionAttributeValues serve both the UpdateExpression and the
	// ConditionExpression, matching real DynamoDB. Gate the mutation on the
	// condition (evaluated against the current item) before applying actions.
	if !h.gateCondition(r.Context(), w, req.TableName, key,
		req.ConditionExpression, req.ExpressionAttributeNames, vals) {
		return
	}

	// Capture the pre-update image so ALL_OLD/UPDATED_OLD/UPDATED_NEW can report
	// the changed attributes relative to it.
	old := h.previousItem(r.Context(), req.TableName, key)

	// The raw UpdateExpression flows to the driver, which parses and evaluates
	// the full grammar (SET arithmetic, if_not_exists, list_append, ADD, DELETE)
	// against the stored item.
	input := dbdriver.UpdateItemInput{
		Table:            req.TableName,
		Key:              key,
		UpdateExpression: req.UpdateExpression,
		ExprNames:        req.ExpressionAttributeNames,
		ExprValues:       vals,
	}

	updated, err := h.db.UpdateItem(r.Context(), input)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := map[string]any{}
	if attrs := updateReturnValues(req.ReturnValues, old, updated); attrs != nil {
		resp["Attributes"] = toWireItem(attrs)
	}

	addConsumedCapacity(resp, req.ReturnConsumedCapacity, req.TableName)
	wire.WriteJSON(w, resp)
}

// updateReturnValues resolves the UpdateItem ReturnValues mode into the
// Attributes map DynamoDB returns: ALL_OLD/ALL_NEW echo the whole pre/post
// image, while UPDATED_OLD/UPDATED_NEW return only the attributes the update
// changed (their old / new values). NONE (or empty) returns nil.
func updateReturnValues(mode string, old, updated map[string]any) map[string]any {
	switch strings.ToUpper(mode) {
	case "ALL_NEW":
		return updated
	case "ALL_OLD":
		return old
	case "UPDATED_NEW":
		return changedAttributes(old, updated, false)
	case "UPDATED_OLD":
		return changedAttributes(old, updated, true)
	default:
		return nil
	}
}

// changedAttributes returns the attributes that differ between old and updated.
// When wantOld is set it returns their previous values (UPDATED_OLD), otherwise
// their new values (UPDATED_NEW). A removed attribute contributes to UPDATED_OLD
// only; an added one to UPDATED_NEW only.
func changedAttributes(old, updated map[string]any, wantOld bool) map[string]any {
	out := map[string]any{}

	for k, nv := range updated {
		if ov, ok := old[k]; !ok || !equalAttr(ov, nv) {
			if wantOld {
				if ov, ok := old[k]; ok {
					out[k] = ov
				}
			} else {
				out[k] = nv
			}
		}
	}

	if wantOld {
		for k, ov := range old {
			if _, ok := updated[k]; !ok {
				out[k] = ov
			}
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// equalAttr compares two decoded attribute values for equality. It covers the
// scalar and container shapes fromWireItem produces; a mismatch in shape counts
// as unequal, which is safe for change detection.
func equalAttr(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// scan handles Scan (full-table read with optional filters).
func (h *Handler) scan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		IndexName                 string            `json:"IndexName"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		Limit                     int               `json:"Limit"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	vals := fromWireItem(req.ExpressionAttributeValues)

	// Flow the raw FilterExpression to the driver, which parses and evaluates
	// it with full grammar fidelity.
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{
		Table:             req.TableName,
		IndexName:         req.IndexName,
		FilterExpression:  req.FilterExpression,
		ExprNames:         req.ExpressionAttributeNames,
		ExprValues:        vals,
		Limit:             req.Limit,
		ExclusiveStartKey: fromWireItem(req.ExclusiveStartKey),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	paths, perr := expr.ParseProjection(req.ProjectionExpression, req.ExpressionAttributeNames)
	if perr != nil {
		writeErr(w, perr)
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toWireItem(expr.Project(item, paths)))
	}

	resp := map[string]any{
		"Items":        items,
		"Count":        len(items),
		"ScannedCount": result.ScannedCount,
	}
	if result.LastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = toWireItem(result.LastEvaluatedKey)
	}

	addConsumedCapacity(resp, req.ReturnConsumedCapacity, req.TableName)
	wire.WriteJSON(w, resp)
}

// batchWriteItem handles BatchWriteItem (puts/deletes across one or more
// tables in a single request).
func (h *Handler) batchWriteItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestItems map[string][]batchWriteRequest `json:"RequestItems"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	total := 0
	for _, requests := range req.RequestItems {
		total += len(requests)
	}

	if total > maxBatchWriteItems {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException",
			"1 validation error detected: Value at 'requestItems' failed to satisfy constraint: "+
				"Member must have length less than or equal to 25")

		return
	}

	for table, requests := range req.RequestItems {
		if err := h.checkBatchDuplicates(r.Context(), table, requests); err != nil {
			writeErr(w, err)
			return
		}
	}

	for table, requests := range req.RequestItems {
		for i := range requests {
			if err := h.applyBatchWrite(r.Context(), table, &requests[i]); err != nil {
				writeErr(w, err)
				return
			}
		}
	}

	wire.WriteJSON(w, map[string]any{"UnprocessedItems": map[string]any{}})
}

// checkBatchDuplicates rejects a BatchWriteItem table whose request list targets
// the same item key more than once, matching DynamoDB's
// "Provided list of item keys contains duplicates" ValidationException.
func (h *Handler) checkBatchDuplicates(ctx context.Context, table string, requests []batchWriteRequest) error {
	cfg, err := h.db.DescribeTable(ctx, table)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(requests))

	for i := range requests {
		src := batchRequestKeySource(&requests[i])
		if src == nil {
			continue
		}

		id := keyIdentity(cfg, src)
		if _, dup := seen[id]; dup {
			return cerrors.New(cerrors.InvalidArgument,
				"Provided list of item keys contains duplicates")
		}

		seen[id] = struct{}{}
	}

	return nil
}

// batchRequestKeySource returns the decoded key-bearing map for a batch write
// request: the item for a put, the key for a delete.
func batchRequestKeySource(req *batchWriteRequest) map[string]any {
	switch {
	case req.PutRequest != nil:
		return fromWireItem(req.PutRequest.Item)
	case req.DeleteRequest != nil:
		return fromWireItem(req.DeleteRequest.Key)
	default:
		return nil
	}
}

// keyIdentity builds a stable identity string from an item's primary-key
// attributes, used to detect two operations targeting the same item.
func keyIdentity(cfg *dbdriver.TableConfig, item map[string]any) string {
	id := fmt.Sprintf("%v", item[cfg.PartitionKey])
	if cfg.SortKey != "" {
		id += "\x00" + fmt.Sprintf("%v", item[cfg.SortKey])
	}

	return id
}

type batchWriteRequest struct {
	PutRequest    *batchPutReq    `json:"PutRequest,omitempty"`
	DeleteRequest *batchDeleteReq `json:"DeleteRequest,omitempty"`
}

type batchPutReq struct {
	Item map[string]any `json:"Item"`
}

type batchDeleteReq struct {
	Key map[string]any `json:"Key"`
}

func (h *Handler) applyBatchWrite(ctx context.Context, table string, req *batchWriteRequest) error {
	switch {
	case req.PutRequest != nil:
		return h.db.PutItem(ctx, table, fromWireItem(req.PutRequest.Item))
	case req.DeleteRequest != nil:
		return h.db.DeleteItem(ctx, table, fromWireItem(req.DeleteRequest.Key))
	default:
		return nil
	}
}

// batchGetItem handles BatchGetItem (gets across one or more tables). Each
// per-table entry may carry a ProjectionExpression (with ExpressionAttributeNames
// placeholders); when present, only the named attributes are returned for that
// table's items — otherwise all attributes are returned.
func (h *Handler) batchGetItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestItems map[string]struct {
			Keys                     []map[string]any  `json:"Keys"`
			ProjectionExpression     string            `json:"ProjectionExpression"`
			ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
		} `json:"RequestItems"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	total := 0
	for _, spec := range req.RequestItems {
		total += len(spec.Keys)
	}

	if total > maxBatchGetItems {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException",
			"Too many items requested for the BatchGetItem call")

		return
	}

	responses := make(map[string][]map[string]any, len(req.RequestItems))

	for table, spec := range req.RequestItems {
		paths, perr := expr.ParseProjection(spec.ProjectionExpression, spec.ExpressionAttributeNames)
		if perr != nil {
			writeErr(w, perr)
			return
		}

		keys := make([]map[string]any, 0, len(spec.Keys))
		for _, k := range spec.Keys {
			keys = append(keys, fromWireItem(k))
		}

		items, err := h.db.BatchGetItems(r.Context(), table, keys)
		if err != nil {
			writeErr(w, err)
			return
		}

		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, toWireItem(expr.Project(item, paths)))
		}

		responses[table] = out
	}

	wire.WriteJSON(w, map[string]any{
		"Responses":       responses,
		"UnprocessedKeys": map[string]any{},
	})
}

// transactGetItems handles TransactGetItems: an ordered, cross-table batch of
// gets. The Responses array mirrors the request order and length, each element
// carrying an Item (or an empty object when that item is absent), exactly as
// real DynamoDB returns.
func (h *Handler) transactGetItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransactItems []struct {
			Get *struct {
				TableName                string            `json:"TableName"`
				Key                      map[string]any    `json:"Key"`
				ProjectionExpression     string            `json:"ProjectionExpression"`
				ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
			} `json:"Get,omitempty"`
		} `json:"TransactItems"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	responses := make([]map[string]any, 0, len(req.TransactItems))

	for _, t := range req.TransactItems {
		if t.Get == nil {
			responses = append(responses, map[string]any{})
			continue
		}

		entry, err := h.transactGetOne(r, t.Get.TableName, t.Get.Key,
			t.Get.ProjectionExpression, t.Get.ExpressionAttributeNames)
		if err != nil {
			writeErr(w, err)
			return
		}

		responses = append(responses, entry)
	}

	wire.WriteJSON(w, map[string]any{"Responses": responses})
}

// transactGetOne resolves a single TransactGetItems Get into its response
// entry: {"Item": ...} when present, {} when the item is missing. A missing
// table is a real error and is propagated.
func (h *Handler) transactGetOne(
	r *http.Request, table string, wireKey map[string]any, projection string, names map[string]string,
) (map[string]any, error) {
	paths, perr := expr.ParseProjection(projection, names)
	if perr != nil {
		return nil, perr
	}

	if _, terr := h.db.DescribeTable(r.Context(), table); terr != nil {
		return nil, terr
	}

	item, err := h.db.GetItem(r.Context(), table, fromWireItem(wireKey))
	if err != nil {
		if cerrors.IsNotFound(err) {
			return map[string]any{}, nil
		}

		return nil, err
	}

	return map[string]any{"Item": toWireItem(expr.Project(item, paths))}, nil
}

// transactOpJSON is the shared wire shape of a single TransactWriteItems
// operation. DynamoDB's four operation kinds (Put, Delete, Update,
// ConditionCheck) draw from this same field set; absent fields decode to zero
// values, so one struct decodes all four.
type transactOpJSON struct {
	TableName                 string            `json:"TableName"`
	Item                      map[string]any    `json:"Item"`
	Key                       map[string]any    `json:"Key"`
	UpdateExpression          string            `json:"UpdateExpression"`
	ConditionExpression       string            `json:"ConditionExpression"`
	ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
	ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
}

// transactWriteJSON is one element of the TransactItems array: exactly one of
// the four operation kinds is set.
type transactWriteJSON struct {
	Put            *transactOpJSON `json:"Put,omitempty"`
	Delete         *transactOpJSON `json:"Delete,omitempty"`
	Update         *transactOpJSON `json:"Update,omitempty"`
	ConditionCheck *transactOpJSON `json:"ConditionCheck,omitempty"`
}

// txOp is a decoded, kind-tagged transaction operation. keySrc carries the
// attributes that identify the target item (the whole item for a Put, the Key
// for the others), used for both condition evaluation and duplicate detection.
type txOp struct {
	kind       string
	table      string
	item       map[string]any
	keySrc     map[string]any
	condition  string
	updateExpr string
	names      map[string]string
	values     map[string]any
}

// transactWriteItems handles TransactWriteItems with full DynamoDB semantics:
// Put/Delete/Update/ConditionCheck operations, per-operation ConditionExpression
// evaluation, rejection of duplicate items, and all-or-nothing application (a
// failed condition cancels the whole transaction with TransactionCanceledException
// and writes nothing).
func (h *Handler) transactWriteItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransactItems []transactWriteJSON `json:"TransactItems"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	ops := normalizeTransactItems(req.TransactItems)

	if err := h.checkTransactDuplicates(ctx, ops); err != nil {
		writeErr(w, err)
		return
	}

	reasons, canceled, err := h.evaluateTransactConditions(ctx, ops)
	if err != nil {
		writeErr(w, err)
		return
	}

	if canceled {
		writeTransactionCancelled(w, reasons)
		return
	}

	if aerr := h.applyTransactOps(ctx, ops); aerr != nil {
		writeTransactErr(w, aerr)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// normalizeTransactItems decodes each wire item into a kind-tagged txOp,
// dropping any element with no operation set.
func normalizeTransactItems(items []transactWriteJSON) []txOp {
	ops := make([]txOp, 0, len(items))

	for i := range items {
		if op, ok := toTxOp(items[i]); ok {
			ops = append(ops, op)
		}
	}

	return ops
}

func toTxOp(item transactWriteJSON) (txOp, bool) {
	switch {
	case item.Put != nil:
		decoded := fromWireItem(item.Put.Item)
		op := buildKeyedOp("Put", item.Put)
		op.item, op.keySrc = decoded, decoded

		return op, true
	case item.Delete != nil:
		return buildKeyedOp("Delete", item.Delete), true
	case item.Update != nil:
		op := buildKeyedOp("Update", item.Update)
		op.updateExpr = item.Update.UpdateExpression

		return op, true
	case item.ConditionCheck != nil:
		return buildKeyedOp("ConditionCheck", item.ConditionCheck), true
	default:
		return txOp{}, false
	}
}

// buildKeyedOp fills the fields common to all operation kinds. Put overrides
// item/keySrc from its Item afterwards.
func buildKeyedOp(kind string, o *transactOpJSON) txOp {
	return txOp{
		kind:      kind,
		table:     o.TableName,
		keySrc:    fromWireItem(o.Key),
		condition: o.ConditionExpression,
		names:     o.ExpressionAttributeNames,
		values:    fromWireItem(o.ExpressionAttributeValues),
	}
}

// checkTransactDuplicates rejects a transaction that targets the same item more
// than once, matching DynamoDB's "Transaction request cannot include multiple
// operations on one item" ValidationException.
func (h *Handler) checkTransactDuplicates(ctx context.Context, ops []txOp) error {
	cfgs := map[string]*dbdriver.TableConfig{}
	seen := make(map[string]struct{}, len(ops))

	for i := range ops {
		cfg, ok := cfgs[ops[i].table]
		if !ok {
			c, err := h.db.DescribeTable(ctx, ops[i].table)
			if err != nil {
				return err
			}

			cfg, cfgs[ops[i].table] = c, c
		}

		id := ops[i].table + "\x00" + keyIdentity(cfg, ops[i].keySrc)
		if _, dup := seen[id]; dup {
			return cerrors.New(cerrors.InvalidArgument,
				"Transaction request cannot include multiple operations on one item")
		}

		seen[id] = struct{}{}
	}

	return nil
}

// evaluateTransactConditions checks every operation's ConditionExpression
// against current state. It returns a per-operation CancellationReasons array
// (in request order) and whether any condition failed. A malformed expression
// or a missing table surfaces as a non-nil error.
func (h *Handler) evaluateTransactConditions(
	ctx context.Context, ops []txOp,
) (reasons []map[string]any, canceled bool, err error) {
	reasons = make([]map[string]any, len(ops))

	for i := range ops {
		reasons[i] = map[string]any{"Code": "None"}

		if ops[i].condition == "" {
			continue
		}

		ok, cerr := h.checkCondition(ctx, ops[i].table, ops[i].keySrc,
			ops[i].condition, ops[i].names, ops[i].values)
		if cerr != nil {
			return nil, false, cerr
		}

		if !ok {
			reasons[i] = map[string]any{"Code": "ConditionalCheckFailed", "Message": "The conditional request failed"}
			canceled = true
		}
	}

	return reasons, canceled, nil
}

// txSnapshot is a pre-image of an item a transaction op will change, kept so a
// mid-apply failure can be rolled back (DynamoDB transactions are all-or-nothing).
type txSnapshot struct {
	table   string
	key     map[string]any
	before  map[string]any
	existed bool
}

// applyTransactOps applies every operation atomically. Conditions have already
// passed; should any op still fail structurally, every already-applied op is
// rolled back so the transaction is all-or-nothing. checkTransactDuplicates
// guarantees each item is touched by at most one op, so the snapshots don't
// alias.
func (h *Handler) applyTransactOps(ctx context.Context, ops []txOp) error {
	snaps := make([]txSnapshot, 0, len(ops))

	for i := range ops {
		if ops[i].kind == "ConditionCheck" {
			continue
		}

		before, err := h.db.GetItem(ctx, ops[i].table, ops[i].keySrc)
		// A missing item (or table) is expected — the op may create it; only a
		// real read failure aborts. Treat NotFound as "no pre-image".
		if err != nil && !cerrors.IsNotFound(err) {
			h.rollbackTransact(ctx, snaps)
			return err
		}

		snaps = append(snaps, txSnapshot{ops[i].table, ops[i].keySrc, before, err == nil && before != nil})
	}

	for i := range ops {
		if err := h.applyTransactOp(ctx, &ops[i]); err != nil {
			h.rollbackTransact(ctx, snaps)
			return err
		}
	}

	return nil
}

// rollbackTransact restores each snapshotted item to its pre-image (re-putting
// what existed, deleting what the transaction created), in reverse order.
func (h *Handler) rollbackTransact(ctx context.Context, snaps []txSnapshot) {
	for i := len(snaps) - 1; i >= 0; i-- {
		s := snaps[i]
		if s.existed {
			_ = h.db.PutItem(ctx, s.table, s.before)
		} else {
			_ = h.db.DeleteItem(ctx, s.table, s.key)
		}
	}
}

func (h *Handler) applyTransactOp(ctx context.Context, op *txOp) error {
	switch op.kind {
	case "Put":
		return h.db.PutItem(ctx, op.table, op.item)
	case "Delete":
		return h.db.DeleteItem(ctx, op.table, op.keySrc)
	case "Update":
		_, err := h.db.UpdateItem(ctx, dbdriver.UpdateItemInput{
			Table:            op.table,
			Key:              op.keySrc,
			UpdateExpression: op.updateExpr,
			ExprNames:        op.names,
			ExprValues:       op.values,
		})

		return err
	default: // ConditionCheck asserts a condition but writes nothing.
		return nil
	}
}

// writeTransactionCancelled emits the TransactionCanceledException carrying the
// per-operation CancellationReasons array, which SDK clients read to learn which
// condition failed.
func writeTransactionCancelled(w http.ResponseWriter, reasons []map[string]any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(http.StatusBadRequest)

	// "cancelled" is DynamoDB's exact message spelling; keep it verbatim.
	//nolint:errcheck,misspell // best-effort response; verbatim AWS message
	json.NewEncoder(w).Encode(map[string]any{
		"__type":              "com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
		"Message":             "Transaction cancelled, please refer cancellation reasons for specific reasons",
		"CancellationReasons": reasons,
	})
}

// writeTransactErr uses a TransactionCanceledException code so real SDK
// clients recognize transaction failures distinctly from generic errors.
func writeTransactErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) || cerrors.IsAlreadyExists(err) {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"TransactionCanceledException", err.Error())

		return
	}

	writeErr(w, err)
}
