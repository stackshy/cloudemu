// Package dynamodb implements the DynamoDB JSON-RPC protocol as a
// server.Handler. Point the real aws-sdk-go-v2 DynamoDB client at a Server
// registered with this handler and operations work against an in-memory
// database driver.
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

const targetPrefix = "DynamoDB_20120810."

// Handler serves DynamoDB JSON-RPC requests against a database.Database driver.
type Handler struct {
	db dbdriver.Database
}

// New returns a DynamoDB handler backed by db.
func New(db dbdriver.Database) *Handler {
	return &Handler{db: db}
}

// Matches returns true for DynamoDB-shaped requests, identified by an
// X-Amz-Target header of "DynamoDB_20120810.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches DynamoDB operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if h.routeTables(w, r, op) || h.routeItems(w, r, op) || h.routeBatch(w, r, op) ||
		h.routeTags(w, r, op) || h.routeTTL(w, r, op) {
		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest,
		"UnknownOperationException", "unknown operation: "+op)
}

func (h *Handler) routeTables(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateTable":
		h.createTable(w, r)
	case "DeleteTable":
		h.deleteTable(w, r)
	case "DescribeTable":
		h.describeTable(w, r)
	case "ListTables":
		h.listTables(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeItems(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutItem":
		h.putItem(w, r)
	case "GetItem":
		h.getItem(w, r)
	case "DeleteItem":
		h.deleteItem(w, r)
	case "UpdateItem":
		h.updateItem(w, r)
	case "Query":
		h.query(w, r)
	case "Scan":
		h.scan(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeBatch(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "BatchWriteItem":
		h.batchWriteItem(w, r)
	case "BatchGetItem":
		h.batchGetItem(w, r)
	case "TransactWriteItems":
		h.transactWriteItems(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
		KeySchema []struct {
			AttributeName string `json:"AttributeName"`
			KeyType       string `json:"KeyType"`
		} `json:"KeySchema"`
		AttributeDefinitions []struct {
			AttributeName string `json:"AttributeName"`
			AttributeType string `json:"AttributeType"`
		} `json:"AttributeDefinitions"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg := dbdriver.TableConfig{Name: req.TableName}

	for _, ks := range req.KeySchema {
		if ks.KeyType == "HASH" {
			cfg.PartitionKey = ks.AttributeName
		}

		if ks.KeyType == "RANGE" {
			cfg.SortKey = ks.AttributeName
		}
	}

	if err := h.db.CreateTable(r.Context(), cfg); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"TableDescription": map[string]any{
			"TableName":   req.TableName,
			"TableStatus": "ACTIVE",
			"KeySchema":   req.KeySchema,
		},
	})
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.db.DeleteTable(r.Context(), req.TableName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"TableDescription": map[string]any{
			"TableName":   req.TableName,
			"TableStatus": "DELETING",
		},
	})
}

func (h *Handler) describeTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg, err := h.db.DescribeTable(r.Context(), req.TableName)
	if err != nil {
		writeErr(w, err)
		return
	}

	keySchema := []map[string]string{
		{"AttributeName": cfg.PartitionKey, "KeyType": "HASH"},
	}

	if cfg.SortKey != "" {
		keySchema = append(keySchema, map[string]string{
			"AttributeName": cfg.SortKey, "KeyType": "RANGE",
		})
	}

	wire.WriteJSON(w, map[string]any{
		"Table": map[string]any{
			"TableName":   cfg.Name,
			"TableStatus": "ACTIVE",
			"KeySchema":   keySchema,
		},
	})
}

func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"TableNames": tables,
	})
}

//nolint:dupl // mirrors deleteItem's condition-gated write path over Item, not Key.
func (h *Handler) putItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Item                      map[string]any    `json:"Item"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	item := fromWireItem(req.Item)
	vals := fromWireItem(req.ExpressionAttributeValues)

	if !h.gateCondition(r.Context(), w, req.TableName, item,
		req.ConditionExpression, req.ExpressionAttributeNames, vals) {
		return
	}

	if err := h.db.PutItem(r.Context(), req.TableName, item); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// gateCondition evaluates a ConditionExpression and, on failure, writes the
// matching DynamoDB error response. It returns true when the caller should
// proceed with the mutation. Shared by PutItem, UpdateItem and DeleteItem.
func (h *Handler) gateCondition(
	ctx context.Context,
	w http.ResponseWriter,
	table string,
	keySource map[string]any,
	condExpr string,
	names map[string]string,
	values map[string]any,
) bool {
	ok, err := h.checkCondition(ctx, table, keySource, condExpr, names, values)
	if err != nil {
		writeErr(w, err)
		return false
	}

	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"ConditionalCheckFailedException", "The conditional request failed")
		return false
	}

	return true
}

// checkCondition evaluates a DynamoDB ConditionExpression against the current
// stored item, using the full expression grammar (functions, boolean
// operators, IN/BETWEEN, size(), type-aware comparisons). keySource carries
// the item's key attributes (the full item for PutItem, the Key map for
// Update/Delete). A missing item is evaluated against an empty item, so
// attribute_not_exists is true and attribute_exists is false — matching real
// DynamoDB create-if-absent semantics. A malformed expression is a
// cerrors.InvalidArgument; a well-formed but unmet condition returns false.
func (h *Handler) checkCondition(
	ctx context.Context,
	table string,
	keySource map[string]any,
	condExpr string,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	condExpr = strings.TrimSpace(condExpr)
	if condExpr == "" {
		return true, nil
	}

	cfg, err := h.db.DescribeTable(ctx, table)
	if err != nil {
		return false, err
	}

	key := map[string]any{cfg.PartitionKey: keySource[cfg.PartitionKey]}
	if cfg.SortKey != "" {
		key[cfg.SortKey] = keySource[cfg.SortKey]
	}

	existing, err := h.db.GetItem(ctx, table, key)
	if err != nil && !cerrors.IsNotFound(err) {
		return false, err
	}

	node, err := expr.ParseCondition(condExpr, names, values)
	if err != nil {
		return false, err
	}

	if existing == nil {
		existing = map[string]any{}
	}

	return expr.Eval(node, existing)
}

func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                string            `json:"TableName"`
		Key                      map[string]any    `json:"Key"`
		ProjectionExpression     string            `json:"ProjectionExpression"`
		ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key := fromWireItem(req.Key)

	// Validate the projection before the lookup so a malformed expression is a
	// ValidationException regardless of whether the item exists, matching real
	// DynamoDB.
	paths, perr := expr.ParseProjection(req.ProjectionExpression, req.ExpressionAttributeNames)
	if perr != nil {
		writeErr(w, perr)
		return
	}

	item, err := h.db.GetItem(r.Context(), req.TableName, key)
	if err != nil {
		// DynamoDB returns an empty response for missing items, not an error.
		if cerrors.IsNotFound(err) {
			wire.WriteJSON(w, map[string]any{})
			return
		}

		writeErr(w, err)

		return
	}

	resp := map[string]any{}
	if item != nil {
		resp["Item"] = toWireItem(expr.Project(item, paths))
	}

	wire.WriteJSON(w, resp)
}

//nolint:dupl // mirrors putItem's condition-gated write path over Key, not Item.
func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key := fromWireItem(req.Key)
	vals := fromWireItem(req.ExpressionAttributeValues)

	if !h.gateCondition(r.Context(), w, req.TableName, key,
		req.ConditionExpression, req.ExpressionAttributeNames, vals) {
		return
	}

	if err := h.db.DeleteItem(r.Context(), req.TableName, key); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) query(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		KeyConditionExpression    string            `json:"KeyConditionExpression"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		Limit                     int               `json:"Limit"`
		ScanIndexForward          *bool             `json:"ScanIndexForward"`
		IndexName                 string            `json:"IndexName"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	vals := fromWireItem(req.ExpressionAttributeValues)
	kc := parseKeyCondition(req.KeyConditionExpression, vals, req.ExpressionAttributeNames)

	forward := true
	if req.ScanIndexForward != nil {
		forward = *req.ScanIndexForward
	}

	// Flow the raw FilterExpression (post key-condition) to the driver, which
	// parses and evaluates it with full grammar fidelity. The KeyCondition
	// path is unchanged.
	result, err := h.db.Query(r.Context(), dbdriver.QueryInput{
		Table:             req.TableName,
		IndexName:         req.IndexName,
		KeyCondition:      kc,
		FilterExpression:  req.FilterExpression,
		ExprNames:         req.ExpressionAttributeNames,
		ExprValues:        vals,
		Limit:             req.Limit,
		SortDescending:    !forward,
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
		"Items": items,
		"Count": len(items),
	}
	if result.LastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = toWireItem(result.LastEvaluatedKey)
	}

	wire.WriteJSON(w, resp)
}

// Sort-key operators emitted for the function/keyword forms of a
// KeyConditionExpression. Relational operators (= < <= > >=) flow through as
// their literal token.
const (
	sortOpBetween    = "BETWEEN"
	sortOpBeginsWith = "BEGINS_WITH"
)

// parseKeyCondition extracts a KeyCondition from a KeyConditionExpression.
// The partition key must use equality ("pk = :v"); the optional sort-key
// condition may be a relational comparison ("sk op :v"), "sk BETWEEN :lo AND
// :hi", or "begins_with(sk, :v)".
func parseKeyCondition(
	keyExpr string,
	vals map[string]any,
	names map[string]string,
) dbdriver.KeyCondition {
	kc := dbdriver.KeyCondition{}
	keyExpr = strings.TrimSpace(keyExpr)

	const andSep = " AND "

	// Split on the FIRST " AND " only: it separates the partition-key equality
	// from the sort-key condition. A BETWEEN sort condition carries its own
	// internal " AND ", which stays with the sort clause.
	andIdx := findCaseInsensitive(keyExpr, andSep)
	pkExpr := keyExpr
	skExpr := ""

	if andIdx >= 0 {
		pkExpr = strings.TrimSpace(keyExpr[:andIdx])
		skExpr = strings.TrimSpace(keyExpr[andIdx+len(andSep):])
	}

	// Partition key: [0]=field, [1]="=", [2]=value placeholder.
	const valueIdx = 2

	pkParts := strings.Fields(pkExpr)
	if len(pkParts) > valueIdx {
		kc.PartitionKey = resolveAttrName(pkParts[0], names)
		kc.PartitionVal = resolveExprVal(pkParts[valueIdx], vals)
	}

	if skExpr != "" {
		parseSortCondition(&kc, skExpr, vals)
	}

	return kc
}

// parseSortCondition fills kc's sort-key fields from the sort portion of a
// KeyConditionExpression. The sort key's attribute name is resolved by the
// provider from the table's key schema, so only the operator and value
// placeholders are extracted here.
func parseSortCondition(kc *dbdriver.KeyCondition, skExpr string, vals map[string]any) {
	if val, ok := parseBeginsWith(skExpr, vals); ok {
		kc.SortOp = sortOpBeginsWith
		kc.SortVal = val

		return
	}

	fields := strings.Fields(skExpr)

	// "sk BETWEEN :lo AND :hi" → [sk BETWEEN :lo AND :hi].
	const (
		betweenLen = 5
		loIdx      = 2
		hiIdx      = 4
	)

	if len(fields) >= betweenLen && strings.EqualFold(fields[1], sortOpBetween) {
		kc.SortOp = sortOpBetween
		kc.SortVal = resolveExprVal(fields[loIdx], vals)
		kc.SortValEnd = resolveExprVal(fields[hiIdx], vals)

		return
	}

	// "sk op :v" → [sk op :v].
	const opLen = 3
	if len(fields) >= opLen {
		kc.SortOp = fields[1]
		kc.SortVal = resolveExprVal(fields[loIdx], vals)
	}
}

// parseBeginsWith recognizes a "begins_with(sk, :v)" sort-key condition and
// returns its resolved value. ok is false when skExpr is not a begins_with
// call.
func parseBeginsWith(skExpr string, vals map[string]any) (val any, ok bool) {
	const fnBeginsWith = "begins_with"
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(skExpr)), fnBeginsWith) {
		return nil, false
	}

	openIdx := strings.Index(skExpr, "(")
	closeIdx := strings.LastIndex(skExpr, ")")

	if openIdx < 0 || closeIdx < openIdx {
		return nil, false
	}

	const argCount = 2

	args := strings.Split(skExpr[openIdx+1:closeIdx], ",")
	if len(args) != argCount {
		return nil, false
	}

	return resolveExprVal(strings.TrimSpace(args[1]), vals), true
}

func findCaseInsensitive(s, substr string) int {
	return strings.Index(strings.ToUpper(s), strings.ToUpper(substr))
}

func resolveAttrName(token string, names map[string]string) string {
	if v, ok := names[token]; ok {
		return v
	}

	return token
}

func resolveExprVal(token string, vals map[string]any) any {
	if v, ok := vals[token]; ok {
		return v
	}

	return token
}

// writeErr maps CloudEmu errors to DynamoDB HTTP error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceInUseException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}
