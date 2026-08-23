package dynamodb

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
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
	if strings.EqualFold(req.ReturnValues, "ALL_NEW") && updated != nil {
		resp["Attributes"] = toWireItem(updated)
	}

	wire.WriteJSON(w, resp)
}

// scan handles Scan (full-table read with optional filters).
func (h *Handler) scan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		Limit                     int               `json:"Limit"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	vals := fromWireItem(req.ExpressionAttributeValues)

	// Flow the raw FilterExpression to the driver, which parses and evaluates
	// it with full grammar fidelity.
	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{
		Table:             req.TableName,
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
		"ScannedCount": len(items),
	}
	if result.LastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = toWireItem(result.LastEvaluatedKey)
	}

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

// batchGetItem handles BatchGetItem (gets across one or more tables).
func (h *Handler) batchGetItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestItems map[string]struct {
			Keys []map[string]any `json:"Keys"`
		} `json:"RequestItems"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	responses := make(map[string][]map[string]any, len(req.RequestItems))

	for table, spec := range req.RequestItems {
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
			out = append(out, toWireItem(item))
		}

		responses[table] = out
	}

	wire.WriteJSON(w, map[string]any{
		"Responses":       responses,
		"UnprocessedKeys": map[string]any{},
	})
}

// transactWriteItems handles TransactWriteItems (grouped puts/deletes).
// Per-table, we split out puts and deletes and send them to the driver's
// single-table TransactWriteItems method. All-or-nothing semantics are the
// driver's responsibility.
func (h *Handler) transactWriteItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransactItems []struct {
			Put *struct {
				TableName string         `json:"TableName"`
				Item      map[string]any `json:"Item"`
			} `json:"Put,omitempty"`
			Delete *struct {
				TableName string         `json:"TableName"`
				Key       map[string]any `json:"Key"`
			} `json:"Delete,omitempty"`
		} `json:"TransactItems"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// Group by table so we can hand one per-table batch to the driver.
	puts := map[string][]map[string]any{}
	deletes := map[string][]map[string]any{}

	for _, t := range req.TransactItems {
		if t.Put != nil {
			puts[t.Put.TableName] = append(puts[t.Put.TableName], fromWireItem(t.Put.Item))
		}

		if t.Delete != nil {
			deletes[t.Delete.TableName] = append(deletes[t.Delete.TableName], fromWireItem(t.Delete.Key))
		}
	}

	// Union the table set and run per-table.
	tables := map[string]struct{}{}
	for t := range puts {
		tables[t] = struct{}{}
	}

	for t := range deletes {
		tables[t] = struct{}{}
	}

	for table := range tables {
		if err := h.db.TransactWriteItems(r.Context(), table, puts[table], deletes[table]); err != nil {
			writeTransactErr(w, err)
			return
		}
	}

	wire.WriteJSON(w, map[string]any{})
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
