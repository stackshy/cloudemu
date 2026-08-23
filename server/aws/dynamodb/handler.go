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
	case "DescribeContinuousBackups":
		h.describeContinuousBackups(w, r)
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
		BillingMode           string `json:"BillingMode"`
		ProvisionedThroughput struct {
			ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
			WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
		} `json:"ProvisionedThroughput"`
		GlobalSecondaryIndexes []struct {
			IndexName string `json:"IndexName"`
			KeySchema []struct {
				AttributeName string `json:"AttributeName"`
				KeyType       string `json:"KeyType"`
			} `json:"KeySchema"`
			Projection struct {
				ProjectionType string `json:"ProjectionType"`
			} `json:"Projection"`
		} `json:"GlobalSecondaryIndexes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg := dbdriver.TableConfig{
		Name:               req.TableName,
		BillingMode:        req.BillingMode,
		ReadCapacityUnits:  req.ProvisionedThroughput.ReadCapacityUnits,
		WriteCapacityUnits: req.ProvisionedThroughput.WriteCapacityUnits,
	}

	for _, gsi := range req.GlobalSecondaryIndexes {
		idx := dbdriver.GSIConfig{Name: gsi.IndexName, Projection: gsi.Projection.ProjectionType}
		for _, ks := range gsi.KeySchema {
			if ks.KeyType == "HASH" {
				idx.PartitionKey = ks.AttributeName
			}
			if ks.KeyType == "RANGE" {
				idx.SortKey = ks.AttributeName
			}
		}
		cfg.GSIs = append(cfg.GSIs, idx)
	}

	for _, ks := range req.KeySchema {
		if ks.KeyType == "HASH" {
			cfg.PartitionKey = ks.AttributeName
		}

		if ks.KeyType == "RANGE" {
			cfg.SortKey = ks.AttributeName
		}
	}

	for _, ad := range req.AttributeDefinitions {
		cfg.Attributes = append(cfg.Attributes,
			dbdriver.AttributeDef{Name: ad.AttributeName, Type: ad.AttributeType})
	}

	if err := h.db.CreateTable(r.Context(), cfg); err != nil {
		writeErr(w, err)
		return
	}

	// Re-describe to pick up the ARN and creation time the provider assigned.
	full, err := h.db.DescribeTable(r.Context(), req.TableName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"TableDescription": tableDescription(full)})
}

// tableDescription builds the DynamoDB TableDescription wire shape that both
// CreateTable and DescribeTable return, including the fields an IaC client reads
// back (ARN, creation time, attribute definitions, billing mode).
func tableDescription(cfg *dbdriver.TableConfig) map[string]any {
	keySchema := []map[string]string{{"AttributeName": cfg.PartitionKey, "KeyType": "HASH"}}
	if cfg.SortKey != "" {
		keySchema = append(keySchema, map[string]string{"AttributeName": cfg.SortKey, "KeyType": "RANGE"})
	}

	attrs := make([]map[string]string, 0, len(cfg.Attributes))
	for _, a := range cfg.Attributes {
		attrs = append(attrs, map[string]string{"AttributeName": a.Name, "AttributeType": a.Type})
	}

	const billingProvisioned = "PROVISIONED"

	billing := cfg.BillingMode
	if billing == "" {
		billing = billingProvisioned
	}

	td := map[string]any{
		"TableName":            cfg.Name,
		"TableStatus":          "ACTIVE",
		"TableArn":             cfg.TableArn,
		"CreationDateTime":     cfg.CreatedAtUnix,
		"KeySchema":            keySchema,
		"AttributeDefinitions": attrs,
		"ItemCount":            0,
		"TableSizeBytes":       0,
		"BillingModeSummary":   map[string]any{"BillingMode": billing},
	}

	if billing == billingProvisioned {
		td["ProvisionedThroughput"] = map[string]any{
			"ReadCapacityUnits":      cfg.ReadCapacityUnits,
			"WriteCapacityUnits":     cfg.WriteCapacityUnits,
			"NumberOfDecreasesToday": 0,
		}
	}

	if gsis := gsiDescriptions(cfg, billing); len(gsis) > 0 {
		td["GlobalSecondaryIndexes"] = gsis
	}

	return td
}

// gsiDescriptions builds the GlobalSecondaryIndexes wire block echoed by
// CreateTable/DescribeTable so an IaC-declared index round-trips (and Query can
// target it via IndexName).
func gsiDescriptions(cfg *dbdriver.TableConfig, billing string) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.GSIs))

	for _, gsi := range cfg.GSIs {
		keySchema := []map[string]string{{"AttributeName": gsi.PartitionKey, "KeyType": "HASH"}}
		if gsi.SortKey != "" {
			keySchema = append(keySchema, map[string]string{"AttributeName": gsi.SortKey, "KeyType": "RANGE"})
		}

		projection := gsi.Projection
		if projection == "" {
			projection = "ALL"
		}

		desc := map[string]any{
			"IndexName":      gsi.Name,
			"IndexStatus":    "ACTIVE",
			"KeySchema":      keySchema,
			"Projection":     map[string]any{"ProjectionType": projection},
			"IndexArn":       cfg.TableArn + "/index/" + gsi.Name,
			"ItemCount":      0,
			"IndexSizeBytes": 0,
		}

		if billing == "PROVISIONED" {
			desc["ProvisionedThroughput"] = map[string]any{
				"ReadCapacityUnits":      cfg.ReadCapacityUnits,
				"WriteCapacityUnits":     cfg.WriteCapacityUnits,
				"NumberOfDecreasesToday": 0,
			}
		}

		out = append(out, desc)
	}

	return out
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

	wire.WriteJSON(w, map[string]any{"Table": tableDescription(cfg)})
}

// describeContinuousBackups reports point-in-time recovery as disabled. IaC
// clients read this on every table refresh; without it the read errors.
func (h *Handler) describeContinuousBackups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if _, err := h.db.DescribeTable(r.Context(), req.TableName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"ContinuousBackupsDescription": map[string]any{
			"ContinuousBackupsStatus": "DISABLED",
			"PointInTimeRecoveryDescription": map[string]any{
				"PointInTimeRecoveryStatus": "DISABLED",
			},
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

	kc, err := parseKeyCondition(req.KeyConditionExpression, vals, req.ExpressionAttributeNames)
	if err != nil {
		writeErr(w, err)
		return
	}

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
// parseKeyCondition parses a KeyConditionExpression into a driver KeyCondition.
// It delegates to the shared expr parser, which uses the real lexer (so it is
// tolerant of spacing) and enforces the restricted key grammar: equality on the
// partition key and one optional sort-key condition (relational, BETWEEN or
// begins_with). The sort-key attribute name is derived by the provider from the
// table's key schema, so it is not carried on the returned condition.
func parseKeyCondition(
	keyExpr string,
	vals map[string]any,
	names map[string]string,
) (dbdriver.KeyCondition, error) {
	kc, err := expr.ParseKeyCondition(keyExpr, names, vals)
	if err != nil {
		return dbdriver.KeyCondition{}, err
	}

	return dbdriver.KeyCondition{
		PartitionKey: kc.PartitionKey,
		PartitionVal: kc.PartitionVal,
		SortOp:       kc.SortOp,
		SortVal:      kc.SortVal,
		SortValEnd:   kc.SortValEnd,
	}, nil
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
