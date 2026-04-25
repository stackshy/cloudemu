// Package dynamodb implements the DynamoDB JSON-RPC protocol as a
// server.Handler. Point the real aws-sdk-go-v2 DynamoDB client at a Server
// registered with this handler and operations work against an in-memory
// database driver.
package dynamodb

import (
	"net/http"
	"strings"

	dbdriver "github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire"
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

	if h.routeTables(w, r, op) || h.routeItems(w, r, op) || h.routeBatch(w, r, op) {
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

func (h *Handler) putItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string         `json:"TableName"`
		Item      map[string]any `json:"Item"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	item := fromWireItem(req.Item)

	if err := h.db.PutItem(r.Context(), req.TableName, item); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string         `json:"TableName"`
		Key       map[string]any `json:"Key"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key := fromWireItem(req.Key)

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
		resp["Item"] = toWireItem(item)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string         `json:"TableName"`
		Key       map[string]any `json:"Key"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	key := fromWireItem(req.Key)

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
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		Limit                     int               `json:"Limit"`
		ScanIndexForward          *bool             `json:"ScanIndexForward"`
		IndexName                 string            `json:"IndexName"`
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

	result, err := h.db.Query(r.Context(), dbdriver.QueryInput{
		Table:        req.TableName,
		IndexName:    req.IndexName,
		KeyCondition: kc,
		Limit:        req.Limit,
		ScanForward:  forward,
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
		"Items": items,
		"Count": result.Count,
	})
}

// parseKeyCondition extracts a KeyCondition from a simple expression.
// Supports: "pk = :v" and "pk = :v AND sk op :v2".
func parseKeyCondition(
	expr string,
	vals map[string]any,
	names map[string]string,
) dbdriver.KeyCondition {
	kc := dbdriver.KeyCondition{}
	expr = strings.TrimSpace(expr)

	const andSep = " AND "

	andIdx := findCaseInsensitive(expr, andSep)
	pkExpr := strings.TrimSpace(expr)
	skExpr := ""

	if andIdx >= 0 {
		pkExpr = strings.TrimSpace(expr[:andIdx])
		skExpr = strings.TrimSpace(expr[andIdx+len(andSep):])
	}

	// Expression fields: [0]=field, [1]=operator, [2]=value placeholder.
	const valueIdx = 2

	pkParts := strings.Fields(pkExpr)
	if len(pkParts) > valueIdx {
		kc.PartitionKey = resolveAttrName(pkParts[0], names)
		kc.PartitionVal = resolveExprVal(pkParts[valueIdx], vals)
	}

	if skExpr != "" {
		skParts := strings.Fields(skExpr)
		if len(skParts) > valueIdx {
			kc.SortOp = skParts[1]
			kc.SortVal = resolveExprVal(skParts[valueIdx], vals)
		}
	}

	return kc
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
