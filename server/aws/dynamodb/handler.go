// Package dynamodb implements the DynamoDB JSON-RPC protocol as a
// server.Handler. Point the real aws-sdk-go-v2 DynamoDB client at a Server
// registered with this handler and operations work against an in-memory
// database driver.
package dynamodb

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

const targetPrefix = "DynamoDB_20120810."

const (
	keyTypeHash        = "HASH"
	keyTypeRange       = "RANGE"
	projectionAll      = "ALL"
	projectionInclude  = "INCLUDE"
	statusEnabled      = "ENABLED"
	statusDisabled     = "DISABLED"
	billingProvisioned = "PROVISIONED"
)

// projectionBlock builds the Projection wire block echoed by a describe. An
// INCLUDE projection also carries the NonKeyAttributes it copied so the
// declared index round-trips exactly.
func projectionBlock(projType string, nonKey []string) map[string]any {
	if projType == "" {
		projType = projectionAll
	}

	block := map[string]any{"ProjectionType": projType}
	if projType == projectionInclude && len(nonKey) > 0 {
		block["NonKeyAttributes"] = nonKey
	}

	return block
}

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
	case "UpdateTable":
		h.updateTable(w, r)
	case "DescribeContinuousBackups":
		h.describeContinuousBackups(w, r)
	case "UpdateContinuousBackups":
		h.updateContinuousBackups(w, r)
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
	case "TransactGetItems":
		h.transactGetItems(w, r)
	default:
		return false
	}

	return true
}

// createTableRequest is the CreateTable wire input, decoded once and mapped to
// a driver TableConfig by buildCreateConfig.
type createTableRequest struct {
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
	GlobalSecondaryIndexes []secondaryIndexJSON `json:"GlobalSecondaryIndexes"`
	LocalSecondaryIndexes  []secondaryIndexJSON `json:"LocalSecondaryIndexes"`
	StreamSpecification    *struct {
		StreamEnabled  bool   `json:"StreamEnabled"`
		StreamViewType string `json:"StreamViewType"`
	} `json:"StreamSpecification"`
	Tags []tagJSON `json:"Tags"`
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	var req createTableRequest

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := validateCreateTableSchema(&req); err != nil {
		writeErr(w, err)
		return
	}

	if err := h.db.CreateTable(r.Context(), buildCreateConfig(&req)); err != nil {
		writeErr(w, err)
		return
	}

	if !h.applyCreateTags(r, w, req.TableName, req.Tags) {
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

// buildCreateConfig maps a decoded CreateTable request onto a driver
// TableConfig (keys, attributes, secondary indexes and the stream spec).
func buildCreateConfig(req *createTableRequest) dbdriver.TableConfig {
	cfg := dbdriver.TableConfig{
		Name:               req.TableName,
		BillingMode:        req.BillingMode,
		ReadCapacityUnits:  req.ProvisionedThroughput.ReadCapacityUnits,
		WriteCapacityUnits: req.ProvisionedThroughput.WriteCapacityUnits,
	}

	for _, gsi := range req.GlobalSecondaryIndexes {
		pk, sk := indexKeys(gsi)
		cfg.GSIs = append(cfg.GSIs, dbdriver.GSIConfig{
			Name: gsi.IndexName, PartitionKey: pk, SortKey: sk,
			Projection: gsi.Projection.ProjectionType, NonKeyAttributes: gsi.Projection.NonKeyAttributes,
		})
	}

	for _, lsi := range req.LocalSecondaryIndexes {
		_, sk := indexKeys(lsi)
		cfg.LSIs = append(cfg.LSIs, dbdriver.LSIConfig{
			Name: lsi.IndexName, SortKey: sk,
			Projection: lsi.Projection.ProjectionType, NonKeyAttributes: lsi.Projection.NonKeyAttributes,
		})
	}

	if req.StreamSpecification != nil && req.StreamSpecification.StreamEnabled {
		cfg.StreamEnabled = true
		cfg.StreamViewType = req.StreamSpecification.StreamViewType
	}

	cfg.PartitionKey, cfg.SortKey = keySchemaKeys(req)

	for _, ad := range req.AttributeDefinitions {
		cfg.Attributes = append(cfg.Attributes,
			dbdriver.AttributeDef{Name: ad.AttributeName, Type: ad.AttributeType})
	}

	return cfg
}

// validateCreateTableSchema enforces the DynamoDB cross-check between KeySchema
// and AttributeDefinitions: every attribute named in the table key schema or any
// secondary-index key schema must be defined in AttributeDefinitions, and every
// AttributeDefinition must be used by some key. AWS rejects a violation with a
// ValidationException carrying the exact wording matched here.
func validateCreateTableSchema(req *createTableRequest) error {
	keyAttrs := map[string]struct{}{}

	for _, ks := range req.KeySchema {
		keyAttrs[ks.AttributeName] = struct{}{}
	}

	for _, idx := range append(append([]secondaryIndexJSON{}, req.GlobalSecondaryIndexes...), req.LocalSecondaryIndexes...) {
		for _, ks := range idx.KeySchema {
			keyAttrs[ks.AttributeName] = struct{}{}
		}
	}

	defined := map[string]struct{}{}
	for _, ad := range req.AttributeDefinitions {
		defined[ad.AttributeName] = struct{}{}
	}

	if missing := firstMissing(keyAttrs, defined); missing != "" {
		return cerrors.Newf(cerrors.InvalidArgument,
			"One or more parameter values were invalid: "+
				"Some index key attributes are not defined in AttributeDefinitions. Missing attributes: %s", missing)
	}

	if unused := firstMissing(defined, keyAttrs); unused != "" {
		return cerrors.New(cerrors.InvalidArgument,
			"One or more parameter values were invalid: "+
				"Number of attributes in KeySchema does not exactly match number of attributes defined in AttributeDefinitions")
	}

	return nil
}

// firstMissing returns one name present in want but absent from have, or "".
func firstMissing(want, have map[string]struct{}) string {
	for name := range want {
		if _, ok := have[name]; !ok {
			return name
		}
	}

	return ""
}

// keySchemaKeys resolves the table's partition and sort key from the request
// key schema.
func keySchemaKeys(req *createTableRequest) (partitionKey, sortKey string) {
	for _, ks := range req.KeySchema {
		if ks.KeyType == keyTypeHash {
			partitionKey = ks.AttributeName
		}

		if ks.KeyType == keyTypeRange {
			sortKey = ks.AttributeName
		}
	}

	return partitionKey, sortKey
}

// secondaryIndexJSON is the shared wire shape of a GSI or LSI on CreateTable.
type secondaryIndexJSON struct {
	IndexName string `json:"IndexName"`
	KeySchema []struct {
		AttributeName string `json:"AttributeName"`
		KeyType       string `json:"KeyType"`
	} `json:"KeySchema"`
	Projection struct {
		ProjectionType   string   `json:"ProjectionType"`
		NonKeyAttributes []string `json:"NonKeyAttributes"`
	} `json:"Projection"`
}

// indexKeys extracts the HASH/RANGE attribute names from an index key schema.
func indexKeys(idx secondaryIndexJSON) (partitionKey, sortKey string) {
	for _, ks := range idx.KeySchema {
		if ks.KeyType == keyTypeHash {
			partitionKey = ks.AttributeName
		}

		if ks.KeyType == keyTypeRange {
			sortKey = ks.AttributeName
		}
	}

	return partitionKey, sortKey
}

// applyCreateTags applies tags supplied on CreateTable so ListTagsOfResource
// returns them, matching real DynamoDB (which tags at create time). It writes
// the error response and returns false on failure.
func (h *Handler) applyCreateTags(r *http.Request, w http.ResponseWriter, table string, tags []tagJSON) bool {
	if len(tags) == 0 {
		return true
	}

	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t.Key] = t.Value
	}

	if err := h.db.TagResource(r.Context(), table, m); err != nil {
		writeErr(w, err)
		return false
	}

	return true
}

// tableDescription builds the DynamoDB TableDescription wire shape that both
// CreateTable and DescribeTable return, including the fields an IaC client reads
// back (ARN, creation time, attribute definitions, billing mode).
func tableDescription(cfg *dbdriver.TableConfig) map[string]any {
	keySchema := []map[string]string{{"AttributeName": cfg.PartitionKey, "KeyType": keyTypeHash}}
	if cfg.SortKey != "" {
		keySchema = append(keySchema, map[string]string{"AttributeName": cfg.SortKey, "KeyType": keyTypeRange})
	}

	attrs := make([]map[string]string, 0, len(cfg.Attributes))
	for _, a := range cfg.Attributes {
		attrs = append(attrs, map[string]string{"AttributeName": a.Name, "AttributeType": a.Type})
	}

	billing := cfg.BillingMode
	if billing == "" {
		billing = billingProvisioned
	}

	td := map[string]any{
		"TableName":            cfg.Name,
		"TableStatus":          "ACTIVE",
		"TableArn":             cfg.TableArn,
		"TableId":              cfg.TableID,
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

	if lsis := lsiDescriptions(cfg); len(lsis) > 0 {
		td["LocalSecondaryIndexes"] = lsis
	}

	if cfg.StreamEnabled {
		td["StreamSpecification"] = map[string]any{
			"StreamEnabled":  true,
			"StreamViewType": cfg.StreamViewType,
		}
		td["LatestStreamArn"] = cfg.StreamArn
		td["LatestStreamLabel"] = cfg.StreamLabel
	}

	return td
}

// lsiDescriptions builds the LocalSecondaryIndexes wire block echoed by
// CreateTable/DescribeTable so an IaC-declared LSI round-trips. An LSI shares
// the table partition key and adds its own sort key.
func lsiDescriptions(cfg *dbdriver.TableConfig) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.LSIs))

	for _, lsi := range cfg.LSIs {
		keySchema := []map[string]string{
			{"AttributeName": cfg.PartitionKey, "KeyType": keyTypeHash},
			{"AttributeName": lsi.SortKey, "KeyType": keyTypeRange},
		}

		out = append(out, map[string]any{
			"IndexName":      lsi.Name,
			"KeySchema":      keySchema,
			"Projection":     projectionBlock(lsi.Projection, lsi.NonKeyAttributes),
			"IndexArn":       cfg.TableArn + "/index/" + lsi.Name,
			"ItemCount":      0,
			"IndexSizeBytes": 0,
		})
	}

	return out
}

// gsiDescriptions builds the GlobalSecondaryIndexes wire block echoed by
// CreateTable/DescribeTable so an IaC-declared index round-trips (and Query can
// target it via IndexName).
func gsiDescriptions(cfg *dbdriver.TableConfig, billing string) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.GSIs))

	for _, gsi := range cfg.GSIs {
		keySchema := []map[string]string{{"AttributeName": gsi.PartitionKey, "KeyType": keyTypeHash}}
		if gsi.SortKey != "" {
			keySchema = append(keySchema, map[string]string{"AttributeName": gsi.SortKey, "KeyType": keyTypeRange})
		}

		desc := map[string]any{
			"IndexName":      gsi.Name,
			"IndexStatus":    "ACTIVE",
			"KeySchema":      keySchema,
			"Projection":     projectionBlock(gsi.Projection, gsi.NonKeyAttributes),
			"IndexArn":       cfg.TableArn + "/index/" + gsi.Name,
			"ItemCount":      0,
			"IndexSizeBytes": 0,
		}

		if billing == billingProvisioned {
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

	// Describe before deleting so the response carries the real ARN, key schema
	// and attribute definitions a client reads back — not a name-only stub.
	full, err := h.db.DescribeTable(r.Context(), req.TableName)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.db.DeleteTable(r.Context(), req.TableName); err != nil {
		writeErr(w, err)
		return
	}

	desc := tableDescription(full)
	desc["TableStatus"] = "DELETING"

	wire.WriteJSON(w, map[string]any{"TableDescription": desc})
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

// describeContinuousBackups reports the table's point-in-time recovery state.
// IaC clients read this on every table refresh; without it the read errors.
// ContinuousBackups is always enabled on a real table, so only the PITR sub-
// status tracks the UpdateContinuousBackups toggle.
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

	pitr := statusDisabled
	if h.pitrEnabled(r.Context(), req.TableName) {
		pitr = statusEnabled
	}

	wire.WriteJSON(w, map[string]any{
		"ContinuousBackupsDescription": map[string]any{
			"ContinuousBackupsStatus": statusEnabled,
			"PointInTimeRecoveryDescription": map[string]any{
				"PointInTimeRecoveryStatus": pitr,
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

func (h *Handler) putItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Item                      map[string]any    `json:"Item"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ReturnValues              string            `json:"ReturnValues"`
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
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

	resp := map[string]any{}

	// ReturnValues=ALL_OLD returns the item as it was before the overwrite, so
	// read it before the mutation.
	if strings.EqualFold(req.ReturnValues, "ALL_OLD") {
		if old := h.previousItem(r.Context(), req.TableName, item); old != nil {
			resp["Attributes"] = toWireItem(old)
		}
	}

	if err := h.db.PutItem(r.Context(), req.TableName, item); err != nil {
		writeErr(w, err)
		return
	}

	addConsumedCapacity(resp, req.ReturnConsumedCapacity, req.TableName)
	wire.WriteJSON(w, resp)
}

// previousItem fetches the stored item identified by keySource's key attributes,
// returning nil when the table or item is missing. Used to satisfy
// ReturnValues=ALL_OLD on Put/Delete without surfacing a lookup error.
func (h *Handler) previousItem(ctx context.Context, table string, keySource map[string]any) map[string]any {
	cfg, err := h.db.DescribeTable(ctx, table)
	if err != nil {
		return nil
	}

	key := map[string]any{cfg.PartitionKey: keySource[cfg.PartitionKey]}
	if cfg.SortKey != "" {
		key[cfg.SortKey] = keySource[cfg.SortKey]
	}

	old, err := h.db.GetItem(ctx, table, key)
	if err != nil {
		return nil
	}

	return old
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
		ReturnConsumedCapacity   string            `json:"ReturnConsumedCapacity"`
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

	// A GetItem against a table that does not exist is a ResourceNotFoundException
	// in real DynamoDB — distinct from a missing item, which returns an empty
	// (200) response. Resolve the table first so the two cases don't conflate.
	if _, terr := h.db.DescribeTable(r.Context(), req.TableName); terr != nil {
		writeErr(w, terr)
		return
	}

	resp := map[string]any{}
	addConsumedCapacity(resp, req.ReturnConsumedCapacity, req.TableName)

	item, err := h.db.GetItem(r.Context(), req.TableName, key)
	if err != nil {
		// DynamoDB returns an empty response for missing items, not an error.
		if cerrors.IsNotFound(err) {
			wire.WriteJSON(w, resp)
			return
		}

		writeErr(w, err)

		return
	}

	if item != nil {
		resp["Item"] = toWireItem(expr.Project(item, paths))
	}

	wire.WriteJSON(w, resp)
}

// addConsumedCapacity adds a ConsumedCapacity block to resp when the request
// asked for it (ReturnConsumedCapacity=TOTAL or INDEXES). The emulator charges a
// nominal one capacity unit — enough for clients that assert the field is
// present and non-nil, which real SDK cost-tracking code does.
func addConsumedCapacity(resp map[string]any, returnConsumed, table string) {
	if !strings.EqualFold(returnConsumed, "TOTAL") && !strings.EqualFold(returnConsumed, "INDEXES") {
		return
	}

	resp["ConsumedCapacity"] = map[string]any{
		"TableName":     table,
		"CapacityUnits": 1.0,
	}
}

func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ReturnValues              string            `json:"ReturnValues"`
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
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

	resp := map[string]any{}

	// ReturnValues=ALL_OLD returns the deleted item, so read it before removal.
	if strings.EqualFold(req.ReturnValues, "ALL_OLD") {
		if old := h.previousItem(r.Context(), req.TableName, key); old != nil {
			resp["Attributes"] = toWireItem(old)
		}
	}

	if err := h.db.DeleteItem(r.Context(), req.TableName, key); err != nil {
		writeErr(w, err)
		return
	}

	addConsumedCapacity(resp, req.ReturnConsumedCapacity, req.TableName)
	wire.WriteJSON(w, resp)
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
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
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
		Table:               req.TableName,
		IndexName:           req.IndexName,
		KeyCondition:        kc,
		FilterExpression:    req.FilterExpression,
		ExprNames:           req.ExpressionAttributeNames,
		ExprValues:          vals,
		Limit:               req.Limit,
		SortDescending:      !forward,
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
	msg := errMessage(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceInUseException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", msg)
	}
}

// errMessage returns the human-readable message for a cloudemu error without
// the internal "Code: " prefix that Error() prepends. Real DynamoDB error
// messages carry no such prefix, so surfacing it would leak an internal detail.
func errMessage(err error) string {
	var e *cerrors.Error
	if errors.As(err, &e) {
		return e.Message
	}

	return err.Error()
}
