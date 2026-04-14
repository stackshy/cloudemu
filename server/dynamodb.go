package server

import (
	"net/http"
	"strings"

	dbdriver "github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
)

const dynamoDBTargetPrefix = "DynamoDB_20120810."

// handleDynamoDB dispatches DynamoDB JSON-RPC requests based on X-Amz-Target.
func (s *Server) handleDynamoDB(w http.ResponseWriter, r *http.Request, target string) {
	op := strings.TrimPrefix(target, dynamoDBTargetPrefix)

	switch op {
	case "CreateTable":
		s.ddbCreateTable(w, r)
	case "DeleteTable":
		s.ddbDeleteTable(w, r)
	case "DescribeTable":
		s.ddbDescribeTable(w, r)
	case "ListTables":
		s.ddbListTables(w, r)
	case "PutItem":
		s.ddbPutItem(w, r)
	case "GetItem":
		s.ddbGetItem(w, r)
	case "DeleteItem":
		s.ddbDeleteItem(w, r)
	case "Query":
		s.ddbQuery(w, r)
	default:
		writeJSONError(w, http.StatusBadRequest, "UnknownOperationException", "unknown operation: "+op)
	}
}

func (s *Server) ddbCreateTable(w http.ResponseWriter, r *http.Request) {
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

	if !decodeJSON(w, r, &req) {
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

	if err := s.drivers.Database.CreateTable(r.Context(), cfg); err != nil {
		writeDDBErr(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"TableDescription": map[string]any{
			"TableName":   req.TableName,
			"TableStatus": "ACTIVE",
			"KeySchema":   req.KeySchema,
		},
	})
}

func (s *Server) ddbDeleteTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := s.drivers.Database.DeleteTable(r.Context(), req.TableName); err != nil {
		writeDDBErr(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"TableDescription": map[string]any{
			"TableName":   req.TableName,
			"TableStatus": "DELETING",
		},
	})
}

func (s *Server) ddbDescribeTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	cfg, err := s.drivers.Database.DescribeTable(r.Context(), req.TableName)
	if err != nil {
		writeDDBErr(w, err)
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

	writeJSON(w, map[string]any{
		"Table": map[string]any{
			"TableName":   cfg.Name,
			"TableStatus": "ACTIVE",
			"KeySchema":   keySchema,
		},
	})
}

func (s *Server) ddbListTables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.drivers.Database.ListTables(r.Context())
	if err != nil {
		writeDDBErr(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"TableNames": tables,
	})
}

func (s *Server) ddbPutItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string         `json:"TableName"`
		Item      map[string]any `json:"Item"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	item := fromDynamoDBItem(req.Item)

	if err := s.drivers.Database.PutItem(r.Context(), req.TableName, item); err != nil {
		writeDDBErr(w, err)
		return
	}

	writeJSON(w, map[string]any{})
}

func (s *Server) ddbGetItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string         `json:"TableName"`
		Key       map[string]any `json:"Key"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	key := fromDynamoDBItem(req.Key)

	item, err := s.drivers.Database.GetItem(r.Context(), req.TableName, key)
	if err != nil {
		// DynamoDB returns an empty response for missing items, not an error.
		if cerrors.IsNotFound(err) {
			writeJSON(w, map[string]any{})
			return
		}

		writeDDBErr(w, err)

		return
	}

	resp := map[string]any{}
	if item != nil {
		resp["Item"] = toDynamoDBItem(item)
	}

	writeJSON(w, resp)
}

func (s *Server) ddbDeleteItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string         `json:"TableName"`
		Key       map[string]any `json:"Key"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	key := fromDynamoDBItem(req.Key)

	if err := s.drivers.Database.DeleteItem(r.Context(), req.TableName, key); err != nil {
		writeDDBErr(w, err)
		return
	}

	writeJSON(w, map[string]any{})
}

func (s *Server) ddbQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		KeyConditionExpression    string            `json:"KeyConditionExpression"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		Limit                     int               `json:"Limit"`
		ScanIndexForward          *bool             `json:"ScanIndexForward"`
		IndexName                 string            `json:"IndexName"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	vals := fromDynamoDBItem(req.ExpressionAttributeValues)
	kc := parseKeyCondition(req.KeyConditionExpression, vals, req.ExpressionAttributeNames)

	forward := true
	if req.ScanIndexForward != nil {
		forward = *req.ScanIndexForward
	}

	result, err := s.drivers.Database.Query(r.Context(), dbdriver.QueryInput{
		Table:        req.TableName,
		IndexName:    req.IndexName,
		KeyCondition: kc,
		Limit:        req.Limit,
		ScanForward:  forward,
	})
	if err != nil {
		writeDDBErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toDynamoDBItem(item))
	}

	writeJSON(w, map[string]any{
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

// findCaseInsensitive returns the index of substr in s (case-insensitive),
// or -1 if not found.
func findCaseInsensitive(s, substr string) int {
	return strings.Index(strings.ToUpper(s), strings.ToUpper(substr))
}

// resolveAttrName resolves an expression attribute name placeholder (#name)
// to its real attribute name.
func resolveAttrName(token string, names map[string]string) string {
	if v, ok := names[token]; ok {
		return v
	}

	return token
}

// resolveExprVal resolves an expression attribute value placeholder (:val)
// to its actual value.
func resolveExprVal(token string, vals map[string]any) any {
	if v, ok := vals[token]; ok {
		return v
	}

	return token
}

// writeDDBErr maps CloudEmu errors to DynamoDB HTTP error responses.
func writeDDBErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeJSONError(w, http.StatusBadRequest, "ResourceInUseException", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}
