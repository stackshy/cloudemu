package dynamodb

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Scan/query filter operator constants.
const (
	OpEqual        = "="
	OpNotEqual     = "!="
	OpLessThan     = "<"
	OpGreaterThan  = ">"
	OpLessEqual    = "<="
	OpGreaterEqual = ">="
	OpContains     = "CONTAINS"
	OpBeginsWith   = "BEGINS_WITH"
	OpBetween      = "BETWEEN"
)

// Stream view type constants.
const (
	ViewNewImage       = "NEW_IMAGE"
	ViewOldImage       = "OLD_IMAGE"
	ViewNewAndOld      = "NEW_AND_OLD_IMAGES"
	ViewKeysOnly       = "KEYS_ONLY"
	maxStreamRecords   = 1000
	defaultStreamLimit = 100
)

// Secondary-index projection types.
const (
	projectionAll      = "ALL"
	projectionKeysOnly = "KEYS_ONLY"
	projectionInclude  = "INCLUDE"
)

var _ driver.Database = (*Mock)(nil)

type tableData struct {
	config        driver.TableConfig
	items         *memstore.Store[map[string]any]
	ttlConfig     driver.TTLConfig
	streamConfig  driver.StreamConfig
	streamRecords []driver.StreamRecord
	seqCounter    atomic.Int64
	tags          map[string]string
	pitrEnabled   bool
}

// Mock is an in-memory mock implementation of DynamoDB.
type Mock struct {
	mu         sync.RWMutex
	tables     map[string]*tableData
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/DynamoDB", MetricName: metricName, Value: value, Unit: "Count",
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new DynamoDB mock.
func New(opts *config.Options) *Mock {
	return &Mock{tables: make(map[string]*tableData), opts: opts}
}

// maxItemSizeBytes is the 400 KB ceiling DynamoDB enforces on a single item
// (the sum of its attribute names and values). A write exceeding it is a
// ValidationException.
const maxItemSizeBytes = 400 * 1024

// validateItemKeys enforces the DynamoDB rule that an item written to a table
// must carry every primary-key attribute, that each key attribute's type
// matches the table's AttributeDefinitions, and that no key attribute holds an
// empty String/Binary value. AWS rejects a violation with a ValidationException
// carrying the exact wording matched here.
func validateItemKeys(cfg driver.TableConfig, item map[string]any) error {
	for _, keyName := range []string{cfg.PartitionKey, cfg.SortKey} {
		if keyName == "" {
			continue
		}

		val, present := item[keyName]
		if !present {
			return cerrors.Newf(cerrors.InvalidArgument,
				"One or more parameter values were invalid: Missing the key %s in the item", keyName)
		}

		if err := validateKeyNotEmpty(keyName, val); err != nil {
			return err
		}

		if err := validateKeyType(cfg, keyName, val); err != nil {
			return err
		}
	}

	return nil
}

// validateKeyNotEmpty rejects an empty-string or empty-binary value on a
// primary-key attribute. A String/Binary attribute may be zero-length only when
// it is NOT used as a table or index key, so a key with an empty value is the
// AWS "cannot contain an empty string/binary value" ValidationException.
func validateKeyNotEmpty(keyName string, val any) error {
	switch v := val.(type) {
	case string:
		if v == "" {
			return cerrors.Newf(cerrors.InvalidArgument,
				"One or more parameter values were invalid: "+
					"The AttributeValue for a key attribute cannot contain an empty string value. Key: %s", keyName)
		}
	case []byte:
		if len(v) == 0 {
			return cerrors.Newf(cerrors.InvalidArgument,
				"One or more parameter values were invalid: "+
					"The AttributeValue for a key attribute cannot contain an empty binary value. Key: %s", keyName)
		}
	}

	return nil
}

// validateItemSize enforces DynamoDB's 400 KB per-item ceiling. The item size
// is the sum of each attribute's name length (UTF-8 bytes) and value size. AWS
// rejects an oversized write with a ValidationException.
func validateItemSize(item map[string]any) error {
	total := 0
	for name, val := range item {
		total += len(name) + valueSize(val)
	}

	if total > maxItemSizeBytes {
		return cerrors.New(cerrors.InvalidArgument, "Item size has exceeded the maximum allowed size")
	}

	return nil
}

// elementOverhead is the nominal per-element byte charge added when sizing a
// list, map or set, approximating DynamoDB's per-element structural cost.
const elementOverhead = 1

// numberSetElementSize is a nominal per-number byte cost used when sizing a
// number set, whose elements are parsed floats with no retained source text.
const numberSetElementSize = 8

// valueSize approximates the on-the-wire byte size DynamoDB attributes to a
// value, enough to enforce the 400 KB item ceiling. Scalars count their raw
// bytes; documents recurse via collectionSize and charge a small per-element
// overhead.
func valueSize(val any) int {
	switch v := val.(type) {
	case string:
		return len(v)
	case expr.Number:
		return len(string(v))
	case []byte:
		return len(v)
	case bool, nil:
		return 1
	default:
		return collectionSize(val)
	}
}

// collectionSize sizes the document and set attribute types, keeping valueSize
// within the cyclomatic-complexity budget. An unrecognized value falls back to
// its formatted string length.
func collectionSize(val any) int {
	switch v := val.(type) {
	case []any:
		total := 0
		for _, e := range v {
			total += valueSize(e) + elementOverhead
		}

		return total
	case map[string]any:
		total := 0
		for name, e := range v {
			total += len(name) + valueSize(e) + elementOverhead
		}

		return total
	case expr.StringSet:
		total := 0
		for _, s := range v {
			total += len(s) + elementOverhead
		}

		return total
	case expr.NumberSet:
		return len(v) * (numberSetElementSize + elementOverhead)
	case expr.BinarySet:
		total := 0
		for _, b := range v {
			total += len(b) + elementOverhead
		}

		return total
	default:
		return len(fmt.Sprintf("%v", v))
	}
}

// validateKeyMapNotEmpty rejects an empty-string/binary value on any key
// attribute supplied in a Key map (UpdateItem, DeleteItem), matching the same
// AWS rule PutItem enforces on the full item.
func validateKeyMapNotEmpty(partitionKey, sortKey string, key map[string]any) error {
	for _, keyName := range []string{partitionKey, sortKey} {
		if keyName == "" {
			continue
		}

		if val, present := key[keyName]; present {
			if err := validateKeyNotEmpty(keyName, val); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateKeyType checks a present key attribute's type against the matching
// AttributeDefinition (when the schema declares one). A mismatch is the AWS
// "Type mismatch for key" ValidationException.
func validateKeyType(cfg driver.TableConfig, keyName string, val any) error {
	for _, def := range cfg.Attributes {
		if def.Name != keyName {
			continue
		}

		if actual := expr.TypeCode(val); actual != def.Type {
			return cerrors.Newf(cerrors.InvalidArgument,
				"One or more parameter values were invalid: Type mismatch for key %s expected: %s actual: %s",
				keyName, def.Type, actual)
		}

		return nil
	}

	return nil
}

func itemKey(cfg driver.TableConfig, item map[string]any) string {
	pk := fmt.Sprintf("%v", item[cfg.PartitionKey])
	if cfg.SortKey != "" {
		return pk + ":" + fmt.Sprintf("%v", item[cfg.SortKey])
	}

	return pk
}

func (m *Mock) CreateTable(_ context.Context, cfg driver.TableConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tables[cfg.Name]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "table %s already exists", cfg.Name)
	}

	cfg.TableArn = idgen.AWSARN("dynamodb", m.opts.Region, m.opts.AccountID, "table/"+cfg.Name)
	cfg.CreatedAtUnix = float64(m.opts.Clock.Now().Unix())
	cfg.TableID = uuidV4()

	td := &tableData{items: memstore.New[map[string]any]()}

	if cfg.StreamEnabled {
		viewType := cfg.StreamViewType
		if viewType == "" {
			viewType = ViewNewAndOld
		}

		cfg.StreamViewType = viewType
		cfg.StreamLabel, cfg.StreamArn = m.newStreamIdentity(cfg.TableArn)
		td.streamConfig = driver.StreamConfig{Enabled: true, ViewType: viewType}
	}

	td.config = cfg
	m.tables[cfg.Name] = td

	return nil
}

// newStreamIdentity builds the (label, ARN) pair a DynamoDB stream carries.
// The label is an ISO-like timestamp and the ARN embeds it, matching the real
// LatestStreamLabel/LatestStreamArn shape clients read back.
func (m *Mock) newStreamIdentity(tableArn string) (label, arn string) {
	label = m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05.000")
	arn = tableArn + "/stream/" + label

	return label, arn
}

// uuidV4 returns a random RFC-4122 v4 UUID, the format a real DynamoDB TableId
// carries. A crypto/rand read failure is unrecoverable for the mock.
func uuidV4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("dynamodb: crypto/rand failed: " + err.Error())
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (m *Mock) DeleteTable(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tables[name]; !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", name)
	}

	delete(m.tables, name)

	return nil
}

func (m *Mock) DescribeTable(_ context.Context, name string) (*driver.TableConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[name]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", name)
	}

	cfg := td.config

	return &cfg, nil
}

func (m *Mock) ListTables(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.tables))
	for name := range m.tables {
		names = append(names, name)
	}

	return names, nil
}

func (m *Mock) PutItem(_ context.Context, table string, item map[string]any) error {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if err := validateItemKeys(td.config, item); err != nil {
		m.mu.Unlock()
		return err
	}

	if err := validateItemSize(item); err != nil {
		m.mu.Unlock()
		return err
	}

	key := itemKey(td.config, item)
	oldItem, hadOld := td.items.Get(key)
	item = maps.Clone(item)
	td.items.Set(key, item)
	m.recordStreamEvent(td, oldItem, item, hadOld)
	m.mu.Unlock()

	dims := map[string]string{"TableName": table}
	m.emitMetric("ConsumedWriteCapacityUnits", 1, dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)

	return nil
}

func (m *Mock) GetItem(_ context.Context, table string, key map[string]any) (map[string]any, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	k := itemKey(td.config, key)
	item, ok := td.items.Get(k)

	if !ok {
		return nil, cerrors.New(cerrors.NotFound, "item not found")
	}

	if m.isItemExpired(td, item) {
		td.items.Delete(k)
		return nil, cerrors.New(cerrors.NotFound, "item not found")
	}

	dims := map[string]string{"TableName": table}
	m.emitMetric("ConsumedReadCapacityUnits", 1, dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)

	return maps.Clone(item), nil
}

// UpdateItem applies partial updates to an existing item.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateItem(_ context.Context, input driver.UpdateItemInput) (map[string]any, error) {
	m.mu.Lock()

	td, exists := m.tables[input.Table]
	if !exists {
		m.mu.Unlock()
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", input.Table)
	}

	if err := validateKeyMapNotEmpty(td.config.PartitionKey, td.config.SortKey, input.Key); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	k := itemKey(td.config, input.Key)
	item, ok := td.items.Get(k)

	// Real DynamoDB UpdateItem upserts: a missing item is created from the key
	// attributes and the update expression, rather than erroring. Any
	// ConditionExpression has already been evaluated by the caller (the wire
	// handler / transaction), so applying here is unconditional.
	var base, oldItem map[string]any
	if ok {
		base = copyItem(item)
		oldItem = copyItem(item)
	} else {
		base = copyItem(input.Key)
	}

	updated, err := driver.ApplyUpdate(base, input)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	td.items.Set(k, updated)
	m.recordStreamEvent(td, oldItem, updated, true)
	m.mu.Unlock()

	dims := map[string]string{"TableName": input.Table}
	m.emitMetric("ConsumedWriteCapacityUnits", 1, dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)

	return maps.Clone(updated), nil
}

func (m *Mock) DeleteItem(_ context.Context, table string, key map[string]any) error {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if err := validateKeyMapNotEmpty(td.config.PartitionKey, td.config.SortKey, key); err != nil {
		m.mu.Unlock()
		return err
	}

	k := itemKey(td.config, key)
	oldItem, hadOld := td.items.Get(k)
	td.items.Delete(k)

	if hadOld {
		m.recordStreamRemove(td, oldItem)
	}

	m.mu.Unlock()

	dims := map[string]string{"TableName": table}
	m.emitMetric("ConsumedWriteCapacityUnits", 1, dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)

	return nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Query(_ context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	td, exists := m.tables[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", input.Table)
	}

	pkField, skField, err := resolveKeyFields(td, input.IndexName)
	if err != nil {
		return nil, err
	}

	// Compile the FilterExpression up front so a malformed expression is
	// rejected before any paging, matching AWS validation.
	node, err := compileFilter(input.FilterExpression, input.ExprNames, input.ExprValues)
	if err != nil {
		return nil, err
	}

	candidates := m.keyMatchedItems(td, pkField, skField, &input)

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	// Order by the queried keys (index keys for GSI queries); the
	// continuation key carries base + index keys like the real service.
	keyFields := []string{td.config.PartitionKey, td.config.SortKey}
	if input.IndexName != "" {
		keyFields = append(keyFields, pkField, skField)
	}
	// Page the key-matched items FIRST — AWS reads up to Limit items and only
	// then applies the FilterExpression. The returned page is the evaluated
	// window, and PageOrdered's LastEvaluatedKey already points at the last
	// evaluated item, present whenever more key-matched items remain (even if
	// every item on this page is later filtered out).
	result, err := driver.PageOrdered(candidates, pkField, skField, keyFields,
		limit, input.PageToken, input.ExclusiveStartKey, input.SortDescending,
		func(it map[string]any) string { return itemKey(td.config, it) })
	if err != nil {
		return nil, err
	}

	// ScannedCount is the number of items evaluated for this page, before the
	// FilterExpression is applied; the filter then trims Items and Count.
	result.ScannedCount = len(result.Items)
	if err = applyFilter(node, input.Filters, result); err != nil {
		return nil, err
	}

	// A GSI query only exposes the index's projected attributes; an LSI query
	// with no projection defaults to ALL_PROJECTED_ATTRIBUTES. When a
	// ProjectionExpression is supplied, an LSI can fetch non-projected base-table
	// attributes, so the full base item is left intact for the wire layer.
	if attrs, filtered := indexProjection(td.config, input.IndexName, input.ProjectionRequested); filtered {
		for i, it := range result.Items {
			result.Items[i] = projectToIndex(it, attrs)
		}
	}

	dims := map[string]string{"TableName": input.Table}
	m.emitMetric("ConsumedReadCapacityUnits", float64(len(result.Items)), dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)

	return result, nil
}

func resolveKeyFields(td *tableData, indexName string) (pkField, skField string, err error) {
	pkField = td.config.PartitionKey
	skField = td.config.SortKey

	if indexName == "" {
		return pkField, skField, nil
	}

	for _, gsi := range td.config.GSIs {
		if gsi.Name == indexName {
			return gsi.PartitionKey, gsi.SortKey, nil
		}
	}

	// An LSI shares the table partition key and defines an alternate sort key.
	for _, lsi := range td.config.LSIs {
		if lsi.Name == indexName {
			return td.config.PartitionKey, lsi.SortKey, nil
		}
	}

	return "", "", cerrors.Newf(cerrors.NotFound, "index %s not found", indexName)
}

// indexProjection returns the attribute set a secondary-index query/scan may
// surface, and whether that set must be enforced. A GSI always enforces its
// projection: a query on a GSI cannot fetch base-table attributes that are not
// projected — KEYS_ONLY exposes only the table and index key attributes,
// INCLUDE adds the named non-key attributes, ALL passes everything through.
//
// An LSI is different (per the LSI developer guide): it can transparently fetch
// non-projected attributes from the base table. When the caller supplied a
// ProjectionExpression (projectionRequested), the full base item — which
// CloudEmu already holds in memory — is returned untouched so the wire layer can
// select any attribute, matching AWS's functional result (CloudEmu does not
// model the extra throughput cost). With no projection an index query defaults
// to ALL_PROJECTED_ATTRIBUTES, so an LSI is still trimmed to its projected set.
//
// A nil-or-ALL projection returns filtered=false so items are untouched. Caller
// must hold at least td's read lock (reads td.config only).
func indexProjection(cfg driver.TableConfig, indexName string, projectionRequested bool) (attrs map[string]struct{}, filtered bool) {
	if indexName == "" {
		return nil, false
	}

	if projectionRequested && isLSI(cfg, indexName) {
		return nil, false
	}

	projType, idxPK, idxSK, nonKey := lookupIndexProjection(cfg, indexName)
	if projType == "" || strings.EqualFold(projType, projectionAll) {
		return nil, false
	}

	attrs = map[string]struct{}{}
	addAttr(attrs, cfg.PartitionKey)
	addAttr(attrs, cfg.SortKey)
	addAttr(attrs, idxPK)
	addAttr(attrs, idxSK)

	if strings.EqualFold(projType, projectionInclude) {
		for _, a := range nonKey {
			addAttr(attrs, a)
		}
	}

	return attrs, true
}

// isLSI reports whether indexName names a local secondary index on the table.
func isLSI(cfg driver.TableConfig, indexName string) bool {
	for _, lsi := range cfg.LSIs {
		if lsi.Name == indexName {
			return true
		}
	}

	return false
}

// lookupIndexProjection resolves an index's projection type, key attributes and
// (for INCLUDE) non-key attributes. An LSI shares the table partition key.
func lookupIndexProjection(cfg driver.TableConfig, indexName string) (projType, idxPK, idxSK string, nonKey []string) {
	for _, gsi := range cfg.GSIs {
		if gsi.Name == indexName {
			return gsi.Projection, gsi.PartitionKey, gsi.SortKey, gsi.NonKeyAttributes
		}
	}

	for _, lsi := range cfg.LSIs {
		if lsi.Name == indexName {
			return lsi.Projection, cfg.PartitionKey, lsi.SortKey, lsi.NonKeyAttributes
		}
	}

	return "", "", "", nil
}

// addAttr records a non-empty attribute name in the set.
func addAttr(attrs map[string]struct{}, name string) {
	if name != "" {
		attrs[name] = struct{}{}
	}
}

// projectToIndex returns a copy of item retaining only the attributes an index
// projects. Attributes absent from the item are simply not present, matching
// the sparse nature of index projections.
func projectToIndex(item map[string]any, attrs map[string]struct{}) map[string]any {
	out := make(map[string]any, len(attrs))

	for name := range attrs {
		if v, ok := item[name]; ok {
			out[name] = v
		}
	}

	return out
}

// keyMatchedItems returns the live (non-expired) items whose key attributes
// satisfy the KeyConditionExpression, in arbitrary order. The FilterExpression
// is deliberately NOT applied here: AWS reads up to Limit of these items and
// only then filters, so filtering happens after paging (see applyFilter).
func (m *Mock) keyMatchedItems(
	td *tableData, pkField, skField string, input *driver.QueryInput,
) []map[string]any {
	var matched []map[string]any

	for _, item := range td.items.All() {
		if m.isItemExpired(td, item) {
			continue
		}

		if !matchesKeyCondition(item, pkField, skField, input) {
			continue
		}

		matched = append(matched, item)
	}

	return matched
}

// applyFilter applies the FilterExpression (or legacy Filters) to an
// already-paged result in place: it trims Items to those that pass and updates
// Count. ScannedCount and LastEvaluatedKey are left untouched — AWS filters
// after reading a page, so a fully filtered-out page still reports the items it
// evaluated and a continuation key. A nil node with no legacy filters is a
// no-op (Count then equals ScannedCount, as AWS documents for unfiltered
// requests).
func applyFilter(node expr.Node, legacy []driver.ScanFilter, result *driver.QueryResult) error {
	if node == nil && len(legacy) == 0 {
		return nil
	}

	kept := result.Items[:0]

	for _, it := range result.Items {
		ok, err := passesFilter(node, legacy, it)
		if err != nil {
			return err
		}

		if ok {
			kept = append(kept, it)
		}
	}

	result.Items = kept
	result.Count = len(kept)

	return nil
}

func matchesKeyCondition(item map[string]any, pkField, skField string, input *driver.QueryInput) bool {
	pkVal := fmt.Sprintf("%v", item[pkField])
	if pkVal != fmt.Sprintf("%v", input.KeyCondition.PartitionVal) {
		return false
	}

	if input.KeyCondition.SortOp != "" && skField != "" {
		skVal := fmt.Sprintf("%v", item[skField])
		condSK := fmt.Sprintf("%v", input.KeyCondition.SortVal)

		if !applySortCondition(skVal, input.KeyCondition.SortOp, condSK, input.KeyCondition.SortValEnd) {
			return false
		}
	}

	return true
}

// compileFilter parses a raw FilterExpression once per request. An empty
// expression yields a nil node, selecting the legacy Filters path.
func compileFilter(filterExpr string, names map[string]string, values map[string]any) (expr.Node, error) {
	if strings.TrimSpace(filterExpr) == "" {
		return nil, nil
	}

	return expr.ParseCondition(filterExpr, names, values)
}

// passesFilter applies the parsed FilterExpression when present, else falls
// back to the legacy flat ScanFilter matching (back-compat for direct driver
// callers that populate Filters instead of FilterExpression).
func passesFilter(node expr.Node, legacy []driver.ScanFilter, item map[string]any) (bool, error) {
	if node != nil {
		return expr.Eval(node, item)
	}

	return matchesFilters(item, legacy), nil
}

// scanCandidates walks the table's items, skipping expired ones and (for an
// index scan) those lacking the index partition key. The FilterExpression is
// applied after paging (AWS reads up to Limit items, then filters), so it is
// not applied here.
func (m *Mock) scanCandidates(
	td *tableData, idxPK string,
) []map[string]any {
	var matched []map[string]any

	for _, item := range td.items.All() {
		if m.isItemExpired(td, item) {
			continue
		}

		if idxPK != "" {
			if _, ok := item[idxPK]; !ok {
				continue
			}
		}

		matched = append(matched, item)
	}

	return matched
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Scan(_ context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	td, exists := m.tables[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", input.Table)
	}

	node, err := compileFilter(input.FilterExpression, input.ExprNames, input.ExprValues)
	if err != nil {
		return nil, err
	}

	// A scan on a secondary index only visits items carrying that index's
	// partition key (a sparse index), so resolve it up front — this also
	// validates the index exists.
	var idxPK string

	if input.IndexName != "" {
		if idxPK, _, err = resolveKeyFields(td, input.IndexName); err != nil {
			return nil, err
		}
	}

	candidates := m.scanCandidates(td, idxPK)

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	// Page FIRST, then filter — AWS reads up to Limit items and applies the
	// FilterExpression to that evaluated window (see Query for the full note).
	result, err := driver.PageOrdered(candidates,
		td.config.PartitionKey, td.config.SortKey,
		[]string{td.config.PartitionKey, td.config.SortKey},
		limit, input.PageToken, input.ExclusiveStartKey, false,
		func(it map[string]any) string { return itemKey(td.config, it) })
	if err != nil {
		return nil, err
	}

	result.ScannedCount = len(result.Items)
	if err = applyFilter(node, input.Filters, result); err != nil {
		return nil, err
	}

	if attrs, filtered := indexProjection(td.config, input.IndexName, input.ProjectionRequested); filtered {
		for i, it := range result.Items {
			result.Items[i] = projectToIndex(it, attrs)
		}
	}

	dims := map[string]string{"TableName": input.Table}
	m.emitMetric("ConsumedReadCapacityUnits", float64(len(result.Items)), dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)

	return result, nil
}

func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]any) error {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	for _, item := range items {
		if err := validateItemKeys(td.config, item); err != nil {
			m.mu.Unlock()
			return err
		}

		if err := validateItemSize(item); err != nil {
			m.mu.Unlock()
			return err
		}
	}

	for _, item := range items {
		key := itemKey(td.config, item)
		oldItem, hadOld := td.items.Get(key)
		item = maps.Clone(item)
		td.items.Set(key, item)

		m.recordStreamEvent(td, oldItem, item, hadOld)
	}

	m.mu.Unlock()

	return nil
}

func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]any) ([]map[string]any, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	var results []map[string]any

	for _, key := range keys {
		if item, ok := td.items.Get(itemKey(td.config, key)); ok {
			results = append(results, maps.Clone(item))
		}
	}

	return results, nil
}

func compareValues(a, b string) int {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)

	if errA == nil && errB == nil {
		if fa < fb {
			return -1
		}

		if fa > fb {
			return 1
		}

		return 0
	}

	if a < b {
		return -1
	}

	if a > b {
		return 1
	}

	return 0
}

func applySortCondition(itemVal, op, condVal string, condValEnd any) bool {
	switch op {
	case OpEqual:
		return itemVal == condVal
	case OpLessThan:
		return compareValues(itemVal, condVal) < 0
	case OpGreaterThan:
		return compareValues(itemVal, condVal) > 0
	case OpLessEqual:
		return compareValues(itemVal, condVal) <= 0
	case OpGreaterEqual:
		return compareValues(itemVal, condVal) >= 0
	case OpBeginsWith:
		return strings.HasPrefix(itemVal, condVal)
	case OpBetween:
		endVal := fmt.Sprintf("%v", condValEnd)
		return compareValues(itemVal, condVal) >= 0 && compareValues(itemVal, endVal) <= 0
	default:
		return false
	}
}

func matchesFilters(item map[string]any, filters []driver.ScanFilter) bool {
	for _, f := range filters {
		if !matchesSingleScanFilter(item, f) {
			return false
		}
	}

	return true
}

func matchesSingleScanFilter(item map[string]any, f driver.ScanFilter) bool {
	val := fmt.Sprintf("%v", item[f.Field])
	condVal := fmt.Sprintf("%v", f.Value)

	switch f.Op {
	case OpEqual:
		return val == condVal
	case OpNotEqual:
		return val != condVal
	case OpLessThan:
		return compareValues(val, condVal) < 0
	case OpGreaterThan:
		return compareValues(val, condVal) > 0
	case OpLessEqual:
		return compareValues(val, condVal) <= 0
	case OpGreaterEqual:
		return compareValues(val, condVal) >= 0
	case OpContains:
		return strings.Contains(val, condVal)
	case OpBeginsWith:
		return strings.HasPrefix(val, condVal)
	default:
		return false
	}
}

// UpdateTTL configures TTL for a table.
func (m *Mock) UpdateTTL(_ context.Context, table string, cfg driver.TTLConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	td.ttlConfig = cfg

	return nil
}

// DescribeTTL returns the TTL configuration for a table.
func (m *Mock) DescribeTTL(_ context.Context, table string) (*driver.TTLConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	cfg := td.ttlConfig

	return &cfg, nil
}

// UpdateStreamConfig configures streams for a table.
func (m *Mock) UpdateStreamConfig(_ context.Context, table string, cfg driver.StreamConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	td.streamConfig = cfg

	// Mirror the stream state onto the table config so a describe reflects it as
	// LatestStreamArn/Label. Enabling assigns a fresh stream identity; disabling
	// clears it.
	td.config.StreamEnabled = cfg.Enabled
	td.config.StreamViewType = cfg.ViewType

	if cfg.Enabled {
		if td.config.StreamArn == "" {
			td.config.StreamLabel, td.config.StreamArn = m.newStreamIdentity(td.config.TableArn)
		}
	} else {
		td.config.StreamArn = ""
		td.config.StreamLabel = ""
	}

	return nil
}

// UpdateThroughput changes a table's billing mode and provisioned throughput.
// It is an AWS-specific capability (UpdateTable), discovered by the wire handler
// via type assertion, so it is not part of the cross-cloud Database interface.
func (m *Mock) UpdateThroughput(_ context.Context, table, billingMode string, rcu, wcu int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if billingMode != "" {
		td.config.BillingMode = billingMode
	}

	if rcu > 0 {
		td.config.ReadCapacityUnits = rcu
	}

	if wcu > 0 {
		td.config.WriteCapacityUnits = wcu
	}

	return nil
}

// SetPITR toggles point-in-time recovery for a table (UpdateContinuousBackups).
// AWS-specific capability, discovered by type assertion.
func (m *Mock) SetPITR(_ context.Context, table string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	td.pitrEnabled = enabled

	return nil
}

// GetPITR reports whether point-in-time recovery is enabled for a table.
func (m *Mock) GetPITR(_ context.Context, table string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return false, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	return td.pitrEnabled, nil
}

// GetStreamRecords returns stream records after the given token.
func (m *Mock) GetStreamRecords(
	_ context.Context, table string, limit int, token string,
) (*driver.StreamIterator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if !td.streamConfig.Enabled {
		return nil, cerrors.New(cerrors.FailedPrecondition, "streams not enabled")
	}

	return filterStreamRecords(td.streamRecords, limit, token), nil
}

func filterStreamRecords(records []driver.StreamRecord, limit int, token string) *driver.StreamIterator {
	if limit <= 0 {
		limit = defaultStreamLimit
	}

	startIdx := 0

	if token != "" {
		for i, r := range records {
			if r.SequenceNumber == token {
				startIdx = i + 1
				break
			}
		}
	}

	end := startIdx + limit
	if end > len(records) {
		end = len(records)
	}

	result := records[startIdx:end]
	nextToken := ""

	if end < len(records) {
		nextToken = result[len(result)-1].SequenceNumber
	}

	return &driver.StreamIterator{
		ShardID:   "shard-000",
		Records:   result,
		NextToken: nextToken,
	}
}

// TransactWriteItems executes puts and deletes atomically.
func (m *Mock) TransactWriteItems(
	_ context.Context, table string, puts []map[string]any, deletes []map[string]any,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	for _, item := range puts {
		if err := validateItemKeys(td.config, item); err != nil {
			return err
		}

		if err := validateItemSize(item); err != nil {
			return err
		}
	}

	for _, key := range deletes {
		if err := validateKeyMapNotEmpty(td.config.PartitionKey, td.config.SortKey, key); err != nil {
			return err
		}
	}

	m.applyTransactPuts(td, puts)
	m.applyTransactDeletes(td, deletes)

	return nil
}

func (m *Mock) applyTransactPuts(td *tableData, puts []map[string]any) {
	for _, item := range puts {
		key := itemKey(td.config, item)
		oldItem, hadOld := td.items.Get(key)
		item = maps.Clone(item)
		td.items.Set(key, item)
		m.recordStreamEvent(td, oldItem, item, hadOld)
	}
}

func (m *Mock) applyTransactDeletes(td *tableData, deletes []map[string]any) {
	for _, key := range deletes {
		k := itemKey(td.config, key)
		oldItem, hadOld := td.items.Get(k)
		td.items.Delete(k)

		if hadOld {
			m.recordStreamRemove(td, oldItem)
		}
	}
}

// isItemExpired checks if an item has expired based on TTL config.
func (m *Mock) isItemExpired(td *tableData, item map[string]any) bool {
	if !td.ttlConfig.Enabled {
		return false
	}

	ttlVal, ok := item[td.ttlConfig.AttributeName]
	if !ok {
		return false
	}

	ttlUnix := toUnixTimestamp(ttlVal)
	if ttlUnix <= 0 {
		return false
	}

	return m.opts.Clock.Now().Unix() > ttlUnix
}

func toUnixTimestamp(val any) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	default:
		parsed, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
		if err != nil {
			return 0
		}

		return int64(parsed)
	}
}

// recordStreamEvent records an INSERT or MODIFY stream event. Caller must hold m.mu.
func (m *Mock) recordStreamEvent(td *tableData, oldItem, newItem map[string]any, hadOld bool) {
	if !td.streamConfig.Enabled {
		return
	}

	eventType := "INSERT"
	if hadOld {
		eventType = "MODIFY"
	}

	rec := m.buildStreamRecord(td, eventType, oldItem, newItem)
	td.streamRecords = appendStreamRecord(td.streamRecords, &rec)
}

// recordStreamRemove records a REMOVE stream event. Caller must hold m.mu.
func (m *Mock) recordStreamRemove(td *tableData, oldItem map[string]any) {
	if !td.streamConfig.Enabled {
		return
	}

	rec := m.buildStreamRecord(td, "REMOVE", oldItem, nil)
	td.streamRecords = appendStreamRecord(td.streamRecords, &rec)
}

func (m *Mock) buildStreamRecord(
	td *tableData, eventType string, oldItem, newItem map[string]any,
) driver.StreamRecord {
	seq := td.seqCounter.Add(1)
	keys := extractKeys(td.config, oldItem, newItem)

	rec := driver.StreamRecord{
		EventID:        fmt.Sprintf("event-%d", seq),
		EventType:      eventType,
		Table:          td.config.Name,
		Keys:           keys,
		Timestamp:      m.opts.Clock.Now(),
		SequenceNumber: fmt.Sprintf("%d", seq),
	}

	applyViewType(&rec, td.streamConfig.ViewType, oldItem, newItem)

	return rec
}

func extractKeys(cfg driver.TableConfig, oldItem, newItem map[string]any) map[string]any {
	src := newItem
	if src == nil {
		src = oldItem
	}

	keys := map[string]any{cfg.PartitionKey: src[cfg.PartitionKey]}
	if cfg.SortKey != "" {
		keys[cfg.SortKey] = src[cfg.SortKey]
	}

	return keys
}

func applyViewType(rec *driver.StreamRecord, viewType string, oldItem, newItem map[string]any) {
	switch viewType {
	case ViewNewImage:
		rec.NewImage = copyItem(newItem)
	case ViewOldImage:
		rec.OldImage = copyItem(oldItem)
	case ViewNewAndOld:
		rec.NewImage = copyItem(newItem)
		rec.OldImage = copyItem(oldItem)
	case ViewKeysOnly:
	}
}

func copyItem(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}

	cp := make(map[string]any, len(item))
	for k, v := range item {
		cp[k] = v
	}

	return cp
}

func appendStreamRecord(records []driver.StreamRecord, rec *driver.StreamRecord) []driver.StreamRecord {
	records = append(records, *rec)
	if len(records) > maxStreamRecords {
		records = records[len(records)-maxStreamRecords:]
	}

	return records
}

// CreateIndex creates a Global Secondary Index on a table.
func (m *Mock) CreateIndex(_ context.Context, table string, cfg driver.GSIConfig) (*driver.IndexInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "index name must not be empty")
	}

	for _, gsi := range td.config.GSIs {
		if gsi.Name == cfg.Name {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "index %s already exists", cfg.Name)
		}
	}

	td.config.GSIs = append(td.config.GSIs, cfg)

	return &driver.IndexInfo{
		Name:         cfg.Name,
		PartitionKey: cfg.PartitionKey,
		SortKey:      cfg.SortKey,
		Status:       "ACTIVE",
	}, nil
}

// DeleteIndex removes a Global Secondary Index from a table.
func (m *Mock) DeleteIndex(_ context.Context, table, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	for i, gsi := range td.config.GSIs {
		if gsi.Name == indexName {
			td.config.GSIs = append(td.config.GSIs[:i], td.config.GSIs[i+1:]...)
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "index %s not found", indexName)
}

// DescribeIndex returns information about a Global Secondary Index.
func (m *Mock) DescribeIndex(_ context.Context, table, indexName string) (*driver.IndexInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	for _, gsi := range td.config.GSIs {
		if gsi.Name == indexName {
			return &driver.IndexInfo{
				Name:         gsi.Name,
				PartitionKey: gsi.PartitionKey,
				SortKey:      gsi.SortKey,
				Status:       "ACTIVE",
			}, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "index %s not found", indexName)
}

// ListIndexes returns all Global Secondary Indexes for a table.
func (m *Mock) ListIndexes(_ context.Context, table string) ([]driver.IndexInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	indexes := make([]driver.IndexInfo, 0, len(td.config.GSIs))
	for _, gsi := range td.config.GSIs {
		indexes = append(indexes, driver.IndexInfo{
			Name:         gsi.Name,
			PartitionKey: gsi.PartitionKey,
			SortKey:      gsi.SortKey,
			Status:       "ACTIVE",
		})
	}

	return indexes, nil
}

// TagResource sets or replaces tag key/values on a table. Existing keys not
// present in tags are preserved; existing keys present in tags are overwritten.
func (m *Mock) TagResource(_ context.Context, table string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if td.tags == nil {
		td.tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		td.tags[k] = v
	}

	return nil
}

// UntagResource removes the given tag keys from a table. Unknown keys are ignored.
func (m *Mock) UntagResource(_ context.Context, table string, tagKeys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	for _, k := range tagKeys {
		delete(td.tags, k)
	}

	return nil
}

// ListTagsOfResource returns a copy of the tag map for a table.
func (m *Mock) ListTagsOfResource(_ context.Context, table string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	out := make(map[string]string, len(td.tags))
	for k, v := range td.tags {
		out[k] = v
	}

	return out, nil
}
