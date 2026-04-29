package dynamodb

import (
	"context"
	"net/http"
	"strings"

	dbdriver "github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire"
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
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ReturnValues              string            `json:"ReturnValues"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	actions := parseUpdateExpression(req.UpdateExpression,
		fromWireItem(req.ExpressionAttributeValues), req.ExpressionAttributeNames)

	input := dbdriver.UpdateItemInput{
		Table:   req.TableName,
		Key:     fromWireItem(req.Key),
		Actions: actions,
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

// parseUpdateExpression parses a DynamoDB UpdateExpression into driver actions.
// Supports "SET a = :v, b = :w" and "REMOVE c, d" clauses combined.
func parseUpdateExpression(expr string, vals map[string]any, names map[string]string) []dbdriver.UpdateAction {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	var actions []dbdriver.UpdateAction

	for _, clause := range splitClauses(expr) {
		verb, rest := splitVerb(clause)

		switch strings.ToUpper(verb) {
		case "SET":
			actions = append(actions, parseSet(rest, vals, names)...)
		case "REMOVE":
			actions = append(actions, parseRemove(rest, names)...)
		}
	}

	return actions
}

// splitClauses splits an UpdateExpression by keyword boundaries (SET, REMOVE,
// ADD, DELETE). Returns one string per clause.
func splitClauses(expr string) []string {
	upper := strings.ToUpper(expr)

	keywords := []string{"SET", "REMOVE", "ADD", "DELETE"}

	starts := make([]int, 0, len(keywords))
	for _, kw := range keywords {
		starts = append(starts, findKeywordStarts(upper, kw)...)
	}

	if len(starts) == 0 {
		return []string{expr}
	}

	sortInts(starts)

	clauses := make([]string, 0, len(starts))

	for i, s := range starts {
		end := len(expr)
		if i+1 < len(starts) {
			end = starts[i+1]
		}

		clauses = append(clauses, strings.TrimSpace(expr[s:end]))
	}

	return clauses
}

// findKeywordStarts returns every offset in upper where kw appears as a
// standalone word (preceded by a non-ident char, followed by a space).
func findKeywordStarts(upper, kw string) []int {
	var starts []int

	i := 0
	for i < len(upper) {
		j := strings.Index(upper[i:], kw)
		if j < 0 {
			return starts
		}

		abs := i + j
		if isWordStart(upper, abs) && hasSpaceAfter(upper, abs, len(kw)) {
			starts = append(starts, abs)
		}

		i = abs + len(kw)
	}

	return starts
}

func isWordStart(s string, i int) bool {
	return i == 0 || !isIdentByte(s[i-1])
}

func hasSpaceAfter(s string, i, kwLen int) bool {
	return i+kwLen < len(s) && s[i+kwLen] == ' '
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func isIdentByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') || b == '_'
}

// splitVerb splits "SET a = :b, c = :d" → ("SET", "a = :b, c = :d").
func splitVerb(clause string) (verb, rest string) {
	const pair = 2

	parts := strings.SplitN(strings.TrimSpace(clause), " ", pair)
	if len(parts) < pair {
		return parts[0], ""
	}

	return parts[0], parts[1]
}

// parseSet parses "a = :val, b = :other" into SET actions.
func parseSet(rest string, vals map[string]any, names map[string]string) []dbdriver.UpdateAction {
	const pair = 2

	var actions []dbdriver.UpdateAction

	for _, assign := range splitTopLevel(rest, ',') {
		parts := strings.SplitN(assign, "=", pair)
		if len(parts) != pair {
			continue
		}

		field := resolveAttrName(strings.TrimSpace(parts[0]), names)
		valExpr := strings.TrimSpace(parts[1])
		actions = append(actions, dbdriver.UpdateAction{
			Action: "SET",
			Field:  field,
			Value:  resolveExprVal(valExpr, vals),
		})
	}

	return actions
}

// parseRemove parses "a, b, c" into REMOVE actions.
func parseRemove(rest string, names map[string]string) []dbdriver.UpdateAction {
	var actions []dbdriver.UpdateAction

	for _, f := range splitTopLevel(rest, ',') {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}

		actions = append(actions, dbdriver.UpdateAction{
			Action: "REMOVE",
			Field:  resolveAttrName(f, names),
		})
	}

	return actions
}

// splitTopLevel splits by sep at the top level (we don't have nesting here,
// so it's just strings.Split — kept as a separate function in case we add
// list/map value literals later).
func splitTopLevel(s string, sep byte) []string {
	return strings.Split(s, string(sep))
}

// scan handles Scan (full-table read with optional filters).
func (h *Handler) scan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		FilterExpression          string            `json:"FilterExpression"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		Limit                     int               `json:"Limit"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	vals := fromWireItem(req.ExpressionAttributeValues)
	filters := parseFilterExpression(req.FilterExpression, vals, req.ExpressionAttributeNames)

	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{
		Table:   req.TableName,
		Filters: filters,
		Limit:   req.Limit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toWireItem(item))
	}

	wire.WriteJSON(w, map[string]any{
		"Items":        items,
		"Count":        result.Count,
		"ScannedCount": result.Count,
	})
}

// parseFilterExpression turns "a = :v AND b > :w" into driver ScanFilters.
// Supports a single clause or AND-joined clauses.
func parseFilterExpression(expr string, vals map[string]any, names map[string]string) []dbdriver.ScanFilter {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	var filters []dbdriver.ScanFilter

	// A valid filter clause has exactly 3 tokens: field, op, value placeholder.
	const filterTokens = 3

	for _, clause := range splitByUpper(expr, " AND ") {
		parts := strings.Fields(clause)
		if len(parts) < filterTokens {
			continue
		}

		filters = append(filters, dbdriver.ScanFilter{
			Field: resolveAttrName(parts[0], names),
			Op:    parts[1],
			Value: resolveExprVal(parts[2], vals),
		})
	}

	return filters
}

// splitByUpper splits s by sep, matching sep case-insensitively. The upstream
// splitter in the query handler already has the same trick for KeyCondition;
// we duplicate it locally to keep advanced.go self-contained.
func splitByUpper(s, sep string) []string {
	upper := strings.ToUpper(s)

	var parts []string

	start := 0

	for {
		i := strings.Index(upper[start:], strings.ToUpper(sep))
		if i < 0 {
			parts = append(parts, strings.TrimSpace(s[start:]))
			return parts
		}

		parts = append(parts, strings.TrimSpace(s[start:start+i]))
		start += i + len(sep)
	}
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
