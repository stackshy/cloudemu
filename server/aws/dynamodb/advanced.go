package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
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

		ReturnValuesOnConditionCheckFailure string `json:"ReturnValuesOnConditionCheckFailure"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key := fromWireItem(req.Key)
	vals := fromWireItem(req.ExpressionAttributeValues)

	// ExpressionAttributeValues serve both the UpdateExpression and the
	// ConditionExpression, matching real DynamoDB. The condition is evaluated and
	// the update applied atomically inside the provider (single lock hold), so an
	// optimistic-lock update cannot race a concurrent writer.
	cond := dbdriver.Condition{
		Expression: req.ConditionExpression,
		Names:      req.ExpressionAttributeNames,
		Values:     vals,
	}

	// The raw UpdateExpression flows to the driver, which parses and evaluates
	// the full grammar (SET arithmetic, if_not_exists, list_append, ADD, DELETE)
	// against the stored item. old is the pre-update image for
	// ALL_OLD/UPDATED_OLD/UPDATED_NEW.
	input := dbdriver.UpdateItemInput{
		Table:            req.TableName,
		Key:              key,
		UpdateExpression: req.UpdateExpression,
		ExprNames:        req.ExpressionAttributeNames,
		ExprValues:       vals,
	}

	updated, old, err := h.writer().UpdateItemConditional(r.Context(), input, cond)
	if handleConditionalError(w, err, req.ReturnValuesOnConditionCheckFailure) {
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
		Table:               req.TableName,
		IndexName:           req.IndexName,
		FilterExpression:    req.FilterExpression,
		ExprNames:           req.ExpressionAttributeNames,
		ExprValues:          vals,
		Limit:               req.Limit,
		ExclusiveStartKey:   fromWireItem(req.ExclusiveStartKey),
		ProjectionRequested: req.ProjectionExpression != "",
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

	// The whole transaction — every ConditionExpression check and every write —
	// runs under a single hold of the provider's table lock, so it is atomic
	// (all-or-nothing) and isolated from concurrent single-item writes. This
	// replaces the former handler-level evaluate-then-apply, which dropped the
	// lock between the check and the write (a TOCTOU that let two conflicting
	// transactions both commit).
	err := h.writer().TransactWrite(ctx, toDriverTransactOps(ops))

	var canceled *dbdriver.TransactionCanceled
	if errors.As(err, &canceled) {
		writeTransactionCancelled(w, buildCancelReasons(len(ops), canceled.FailedConditions))
		return
	}

	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// toDriverTransactOps maps the decoded wire operations onto the provider's
// atomic transaction ops. For a Put, keySrc equals item (set by toTxOp) and the
// provider keys off the item; for the other kinds keySrc is the operation's Key.
func toDriverTransactOps(ops []txOp) []dbdriver.TransactOp {
	out := make([]dbdriver.TransactOp, len(ops))

	for i := range ops {
		out[i] = dbdriver.TransactOp{
			Kind:  ops[i].kind,
			Table: ops[i].table,
			Item:  ops[i].item,
			Key:   ops[i].keySrc,
			Condition: dbdriver.Condition{
				Expression: ops[i].condition,
				Names:      ops[i].names,
				Values:     ops[i].values,
			},
			UpdateExpression: ops[i].updateExpr,
			ExprNames:        ops[i].names,
			ExprValues:       ops[i].values,
		}
	}

	return out
}

// buildCancelReasons builds the per-operation CancellationReasons array: "None"
// for every operation, overwritten with ConditionalCheckFailed for the indices
// whose condition failed. The shape matches what real DynamoDB returns in a
// TransactionCanceledException.
func buildCancelReasons(n int, failed []int) []map[string]any {
	reasons := make([]map[string]any, n)
	for i := range reasons {
		reasons[i] = map[string]any{"Code": "None"}
	}

	for _, idx := range failed {
		if idx >= 0 && idx < n {
			reasons[idx] = map[string]any{"Code": "ConditionalCheckFailed", "Message": "The conditional request failed"}
		}
	}

	return reasons
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
