package dynamodb

import (
	"context"
	"crypto/rand"
	"fmt"
	"hash/fnv"
	"maps"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/internal/settle"
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

// streamShardID is the emulator's single, always-open DynamoDB Streams shard.
// AWS's real ShardId shape enforces a minimum length of 28 characters
// ("shardId-<20-digit-epoch-ms>-<8-hex-char-suffix>"); a shorter placeholder
// (e.g. "shard-000") is rejected client-side by the AWS SDK/CLI's own request
// validation before the request is ever sent, so GetShardIterator can never be
// called against it. The value mirrors real DynamoDB's format and length; it
// must match server/aws/dynamodb's streamShardID constant.
const streamShardID = "shardId-00000001700000000000-00000001"

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

// DynamoDB table/GSI lifecycle states and their settle durations. A real table
// reports CREATING on create and UPDATING on an UpdateTable before reaching
// ACTIVE; a GSI reports CREATING while it back-fills. The overlay is a read-time
// window (see internal/settle) gated behind config.Options.AsyncSettle: with the
// default (off) SettleDuration returns 0, the window is inactive and every read
// reports ACTIVE immediately — byte-for-byte the historical behavior.
const (
	statusActive   = "ACTIVE"
	statusCreating = "CREATING"
	statusUpdating = "UPDATING"

	settleTableCreate = 2 * time.Second // table CREATING->ACTIVE
	settleTableUpdate = 2 * time.Second // table UPDATING->ACTIVE (throughput/billing change)
	settleGSICreate   = 2 * time.Second // GSI CREATING->ACTIVE
)

// gsiSettleKey is the key under which a table's GSI settle window is stored,
// namespacing the index by its table so identically named indexes on different
// tables never collide.
func gsiSettleKey(table, index string) string {
	return table + "\x00" + index
}

// Mock is an in-memory mock implementation of DynamoDB.
type Mock struct {
	mu            sync.RWMutex
	tables        map[string]*tableData
	opts          *config.Options
	monitoring    mondriver.Monitoring
	streamInvoker StreamEventInvoker
	// tableSettle overlays a CREATING/UPDATING window onto a table's stored
	// ACTIVE status; gsiSettle does the same for each GSI (keyed by
	// gsiSettleKey). Both are self-locking and independent of m.mu, so read-path
	// accessors need no provider lock. Inactive unless config.AsyncSettle is set.
	tableSettle *settle.Set
	gsiSettle   *settle.Set
	// pendingStream buffers stream records captured under m.mu so their delivery
	// runs after the lock is released (a mapped Lambda may write back into
	// DynamoDB); guarded by m.mu.
	pendingStream []pendingStreamEvent
	// txIdempotency records recently used TransactWriteItems ClientRequestTokens
	// (token -> request fingerprint + time) so a replay short-circuits instead of
	// re-applying; guarded by m.mu. Mirrors the SQS FIFO deduplicationIndex.
	txIdempotency map[string]txIdempotencyRecord
	// backups holds on-demand table backups keyed by BackupArn: each captures a
	// table's schema and a deep copy of its items at backup time, so restoring
	// replays them into a new table regardless of later mutations. Guarded by
	// m.mu; see backup.go.
	backups map[string]*backupData
}

// StreamEventInvoker delivers a DynamoDB Streams event batch to whatever Lambda
// event-source-mappings target the stream identified by eventSourceARN. The
// Lambda mock satisfies it, enabling real DynamoDB-stream -> Lambda invocation
// (mirroring the S3 -> Lambda LambdaInvoker wiring).
type StreamEventInvoker interface {
	DeliverEventSourceBatch(ctx context.Context, eventSourceARN string, payload []byte) (delivered bool, err error)
}

// pendingStreamEvent is one captured change record awaiting out-of-lock delivery
// to the stream's Lambda event-source-mappings.
type pendingStreamEvent struct {
	streamARN string
	region    string
	viewType  string
	rec       driver.StreamRecord
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetStreamInvoker wires the Lambda backend so item writes to a stream-enabled
// table invoke the stream's event-source-mapping targets.
func (m *Mock) SetStreamInvoker(i StreamEventInvoker) {
	m.streamInvoker = i
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
	return &Mock{
		tables:        make(map[string]*tableData),
		opts:          opts,
		txIdempotency: make(map[string]txIdempotencyRecord),
		backups:       make(map[string]*backupData),
		tableSettle:   settle.NewSet(),
		gsiSettle:     settle.NewSet(),
	}
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

	return validateIndexKeyTypes(cfg, item)
}

// validateIndexKeyTypes checks every GSI/LSI key attribute present in item
// against its declared AttributeDefinition type. Unlike the table's own
// primary key, an index key attribute is optional on an item — an item that
// omits it simply doesn't appear in that index — but AWS still rejects a type
// mismatch on one that IS present with a ValidationException naming the index.
func validateIndexKeyTypes(cfg driver.TableConfig, item map[string]any) error {
	for _, gsi := range cfg.GSIs {
		for _, keyName := range []string{gsi.PartitionKey, gsi.SortKey} {
			if err := validateIndexKeyType(cfg, gsi.Name, keyName, item); err != nil {
				return err
			}
		}
	}

	for _, lsi := range cfg.LSIs {
		if err := validateIndexKeyType(cfg, lsi.Name, lsi.SortKey, item); err != nil {
			return err
		}
	}

	return nil
}

// validateIndexKeyType checks one index key attribute's value, when the item
// carries it, against its AttributeDefinition type.
func validateIndexKeyType(cfg driver.TableConfig, indexName, keyName string, item map[string]any) error {
	if keyName == "" {
		return nil
	}

	val, present := item[keyName]
	if !present {
		return nil
	}

	for _, def := range cfg.Attributes {
		if def.Name != keyName {
			continue
		}

		if actual := expr.TypeCode(val); actual != def.Type {
			return cerrors.Newf(cerrors.InvalidArgument,
				"One or more parameter values were invalid: Type mismatch for Index Key %s Expected: %s Actual: %s IndexName: %s",
				keyName, def.Type, actual, indexName)
		}

		return nil
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
	if itemSizeBytes(item) > maxItemSizeBytes {
		return cerrors.New(cerrors.InvalidArgument, "Item size has exceeded the maximum allowed size")
	}

	return nil
}

// itemSizeBytes approximates the on-the-wire byte size DynamoDB attributes to an
// item — the sum of each attribute's name length and value size. It backs both
// the 400 KB item-ceiling check and a backup's reported SizeBytes.
func itemSizeBytes(item map[string]any) int {
	total := 0
	for name, val := range item {
		total += len(name) + valueSize(val)
	}

	return total
}

// elementOverhead is the 1 byte DynamoDB charges per List or Map element, on
// top of the element's own value size.
const elementOverhead = 1

// documentContainerOverhead is the flat 3 bytes DynamoDB charges for any List
// or Map attribute, regardless of its contents.
const documentContainerOverhead = 3

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
		total := documentContainerOverhead
		for _, e := range v {
			total += valueSize(e) + elementOverhead
		}

		return total
	case map[string]any:
		total := documentContainerOverhead
		for name, e := range v {
			total += len(name) + valueSize(e) + elementOverhead
		}

		return total
	case expr.StringSet:
		total := 0
		for _, s := range v {
			total += len(s)
		}

		return total
	case expr.NumberSet:
		return len(v) * numberSetElementSize
	case expr.BinarySet:
		total := 0
		for _, b := range v {
			total += len(b)
		}

		return total
	default:
		return len(fmt.Sprintf("%v", v))
	}
}

// validateKeySchema enforces that a standalone Key parameter — GetItem,
// DeleteItem, UpdateItem, BatchGetItem, TransactGetItems/TransactWriteItems —
// names exactly the table's key schema attributes (the partition key, and the
// sort key when the table has one): no fewer, no more, each with a non-empty
// value of the declared type. Real DynamoDB collapses a missing or an
// unrecognized key attribute into one ValidationException; the wire layer
// otherwise silently looks up the wrong (or no) item instead of rejecting the
// call the way real DynamoDB does.
func validateKeySchema(cfg driver.TableConfig, key map[string]any) error {
	want := map[string]struct{}{}
	if cfg.PartitionKey != "" {
		want[cfg.PartitionKey] = struct{}{}
	}

	if cfg.SortKey != "" {
		want[cfg.SortKey] = struct{}{}
	}

	if len(key) != len(want) {
		return newKeySchemaMismatch()
	}

	for name, val := range key {
		if _, ok := want[name]; !ok {
			return newKeySchemaMismatch()
		}

		if err := validateKeyNotEmpty(name, val); err != nil {
			return err
		}

		if err := validateKeyType(cfg, name, val); err != nil {
			return err
		}
	}

	return nil
}

// newKeySchemaMismatch is the ValidationException AWS returns for a Key
// parameter that omits a schema key attribute or names one the schema doesn't
// have, verbatim across every DynamoDB operation that takes a standalone Key.
func newKeySchemaMismatch() error {
	return cerrors.New(cerrors.InvalidArgument,
		"One or more parameter values were invalid: The provided key element does not match the schema")
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
	pk := expr.CanonicalKey(item[cfg.PartitionKey])
	if cfg.SortKey != "" {
		return pk + ":" + expr.CanonicalKey(item[cfg.SortKey])
	}

	return pk
}

func (m *Mock) CreateTable(ctx context.Context, cfg driver.TableConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tables[cfg.Name]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "table %s already exists", cfg.Name)
	}

	cfg.TableArn = idgen.AWSARN("dynamodb", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, "table/"+cfg.Name)
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

	now := m.opts.Clock.Now()
	m.tableSettle.Begin(cfg.Name, statusCreating, now, m.opts.SettleDuration(settleTableCreate))

	gsiDur := m.opts.SettleDuration(settleGSICreate)

	for _, gsi := range cfg.GSIs {
		m.gsiSettle.Begin(gsiSettleKey(cfg.Name, gsi.Name), statusCreating, now, gsiDur)
	}

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

// arnRegion returns the region field of a DynamoDB ARN
// (arn:aws:dynamodb:<region>:<account>:table/<name>), or fallback when the ARN
// is malformed. A table's stored ARN is the source of truth for its region, so
// a stream-delivery event's region is derived from it rather than the configured
// default.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
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

	td, exists := m.tables[name]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "table %s not found", name)
	}

	m.tableSettle.Clear(name)

	for _, gsi := range td.config.GSIs {
		m.gsiSettle.Clear(gsiSettleKey(name, gsi.Name))
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

// PutItem writes item unconditionally. It is the empty-condition case of
// PutItemConditional, so the conditional and unconditional paths share one atomic
// implementation.
func (m *Mock) PutItem(ctx context.Context, table string, item map[string]any) error {
	_, err := m.PutItemConditional(ctx, table, item, driver.Condition{})

	return err
}

func (m *Mock) GetItem(_ context.Context, table string, key map[string]any) (map[string]any, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if err := validateKeySchema(td.config, key); err != nil {
		return nil, err
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

// UpdateItem applies partial updates to an item (upserting when absent). It is
// the empty-condition case of UpdateItemConditional.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateItem(ctx context.Context, input driver.UpdateItemInput) (map[string]any, error) {
	updated, _, err := m.UpdateItemConditional(ctx, input, driver.Condition{})

	return updated, err
}

// DeleteItem removes the item at key unconditionally. It is the empty-condition
// case of DeleteItemConditional.
func (m *Mock) DeleteItem(ctx context.Context, table string, key map[string]any) error {
	_, err := m.DeleteItemConditional(ctx, table, key, driver.Condition{})

	return err
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
// index scan) those lacking the index partition key. For a parallel scan
// (total != nil) only items whose stable primary-key hash falls in segment are
// kept. The FilterExpression is applied after paging (AWS reads up to Limit
// items, then filters), so it is not applied here.
func (m *Mock) scanCandidates(
	td *tableData, idxPK string, segment, total *int32,
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

		if !itemInSegment(itemKey(td.config, item), segment, total) {
			continue
		}

		matched = append(matched, item)
	}

	return matched
}

// maxParallelScanSegments is the DynamoDB ceiling on a parallel scan's
// TotalSegments.
const maxParallelScanSegments = 1000000

// validateScanSegments enforces the DynamoDB parallel-scan rules: Segment and
// TotalSegments are given together, TotalSegments is 1..1000000, and Segment is
// in [0,TotalSegments). Both nil is an ordinary full scan.
func validateScanSegments(segment, total *int32) error {
	if segment == nil && total == nil {
		return nil
	}

	if segment == nil || total == nil {
		return cerrors.New(cerrors.InvalidArgument,
			"The request must contain both Segment and TotalSegments, or neither")
	}

	if *total < 1 || *total > maxParallelScanSegments {
		return cerrors.Newf(cerrors.InvalidArgument,
			"The TotalSegments parameter must be between 1 and %d", maxParallelScanSegments)
	}

	if *segment < 0 || *segment >= *total {
		return cerrors.Newf(cerrors.InvalidArgument,
			"The Segment parameter must be between 0 and TotalSegments-1 (%d)", *total-1)
	}

	return nil
}

// resolveScanIndexPK returns the partition-key attribute of the secondary index
// a scan targets, or "" for a base-table scan. It also validates that a named
// index exists.
func resolveScanIndexPK(td *tableData, indexName string) (string, error) {
	if indexName == "" {
		return "", nil
	}

	pk, _, err := resolveKeyFields(td, indexName)

	return pk, err
}

// itemInSegment reports whether an item belongs to a parallel scan's Segment,
// hashing its primary key stably (FNV-1a) so every item maps to exactly one of
// the TotalSegments shards — making the union of all shards cover the table
// exactly once. A nil total (ordinary scan) always matches.
func itemInSegment(key string, segment, total *int32) bool {
	if total == nil {
		return true
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(key))

	// Compute the shard in int64 so there is no narrowing conversion: the FNV
	// sum widens losslessly, and validateScanSegments guarantees total>=1.
	return int64(h.Sum32())%int64(*total) == int64(*segment)
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

	if err = validateScanSegments(input.Segment, input.TotalSegments); err != nil {
		return nil, err
	}

	// A scan on a secondary index only visits items carrying that index's
	// partition key (a sparse index), so resolve it up front — this also
	// validates the index exists.
	idxPK, err := resolveScanIndexPK(td, input.IndexName)
	if err != nil {
		return nil, err
	}

	candidates := m.scanCandidates(td, idxPK, input.Segment, input.TotalSegments)

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

func (m *Mock) BatchPutItems(ctx context.Context, table string, items []map[string]any) error {
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
	m.flushStreamDeliveries(ctx)

	return nil
}

func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]any) ([]map[string]any, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	for _, key := range keys {
		if err := validateKeySchema(td.config, key); err != nil {
			return nil, err
		}
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

	m.tableSettle.Begin(table, statusUpdating, m.opts.Clock.Now(), m.opts.SettleDuration(settleTableUpdate))

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
		ShardID:   streamShardID,
		Records:   result,
		NextToken: nextToken,
	}
}

// TransactWriteItems executes puts and deletes atomically.
func (m *Mock) TransactWriteItems(
	ctx context.Context, table string, puts []map[string]any, deletes []map[string]any,
) error {
	m.mu.Lock()
	// flush is registered before the unlock defer so it runs after the lock is
	// released (defers are LIFO), delivering stream records outside m.mu.
	defer func() { m.flushStreamDeliveries(ctx) }()
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
		if err := validateKeySchema(td.config, key); err != nil {
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
	m.queueStreamDelivery(td, &rec)
}

// recordStreamRemove records a REMOVE stream event. Caller must hold m.mu.
func (m *Mock) recordStreamRemove(td *tableData, oldItem map[string]any) {
	if !td.streamConfig.Enabled {
		return
	}

	rec := m.buildStreamRecord(td, "REMOVE", oldItem, nil)
	td.streamRecords = appendStreamRecord(td.streamRecords, &rec)
	m.queueStreamDelivery(td, &rec)
}

// queueStreamDelivery buffers a change record for out-of-lock delivery to the
// stream's Lambda event-source-mappings. A no-op when no invoker is wired.
// Caller must hold m.mu.
func (m *Mock) queueStreamDelivery(td *tableData, rec *driver.StreamRecord) {
	if m.streamInvoker == nil || td.config.StreamArn == "" {
		return
	}

	m.pendingStream = append(m.pendingStream, pendingStreamEvent{
		streamARN: td.config.StreamArn,
		region:    arnRegion(td.config.TableArn, m.opts.Region),
		viewType:  td.streamConfig.ViewType,
		rec:       *rec,
	})
}

// flushStreamDeliveries drains the buffered stream records and delivers them to
// the stream's Lambda event-source-mappings, grouping consecutive records of the
// same stream into a single event batch (as a real ESM invoke would). It runs
// after m.mu is released so a mapped Lambda may safely call back into DynamoDB.
//
// callerCtx is the write call's own context, used only to read the re-entrant
// delivery depth (internal/recursionguard): a mapped Lambda commonly writes
// back into its own source table (mark-processed, audit-append, status-bump),
// re-entering here through the very same synchronous call chain
// (Put/Update/DeleteItem -> flush -> DeliverEventSourceBatch -> Invoke ->
// handler -> Put/Update/DeleteItem -> ...). Delivery itself always runs on a
// fresh background context, decoupled from callerCtx's cancellation/deadline
// (delivery must still complete once the write call has already returned),
// carrying forward only the depth so the chain stays bounded.
func (m *Mock) flushStreamDeliveries(callerCtx context.Context) {
	if m.streamInvoker == nil {
		return
	}

	m.mu.Lock()
	pending := m.pendingStream
	m.pendingStream = nil
	m.mu.Unlock()

	ctx := recursionguard.WithDepth(context.Background(), recursionguard.Depth(callerCtx))

	for i := 0; i < len(pending); {
		j := i + 1
		for j < len(pending) && pending[j].streamARN == pending[i].streamARN {
			j++
		}

		batch := make([]driver.StreamRecord, 0, j-i)
		for k := i; k < j; k++ {
			batch = append(batch, pending[k].rec)
		}

		payload := buildLambdaStreamEvent(pending[i].streamARN, pending[i].region, pending[i].viewType, batch)
		_, _ = m.streamInvoker.DeliverEventSourceBatch(ctx, pending[i].streamARN, payload)

		i = j
	}
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

	now := m.opts.Clock.Now()
	key := gsiSettleKey(table, cfg.Name)
	m.gsiSettle.Begin(key, statusCreating, now, m.opts.SettleDuration(settleGSICreate))

	return &driver.IndexInfo{
		Name:         cfg.Name,
		PartitionKey: cfg.PartitionKey,
		SortKey:      cfg.SortKey,
		Status:       m.gsiSettle.State(key, now, statusActive),
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

			m.gsiSettle.Clear(gsiSettleKey(table, indexName))

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
				Status:       m.gsiSettle.State(gsiSettleKey(table, indexName), m.opts.Clock.Now(), statusActive),
			}, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "index %s not found", indexName)
}

// TableStatus reports a table's lifecycle status, overlaying any active
// CREATING/UPDATING settle window onto the stored ACTIVE state. It is the
// read-path accessor the DynamoDB wire handler calls (via an optional
// capability interface) instead of hardcoding "ACTIVE"; absent a window (the
// AsyncSettle-off default, or after the window elapses) it returns ACTIVE.
func (m *Mock) TableStatus(table string) string {
	return m.tableSettle.State(table, m.opts.Clock.Now(), statusActive)
}

// GSIStatus reports a Global Secondary Index's lifecycle status, overlaying any
// active CREATING settle window onto the stored ACTIVE state. Companion to
// TableStatus for the wire handler's GlobalSecondaryIndexes block.
func (m *Mock) GSIStatus(table, index string) string {
	return m.gsiSettle.State(gsiSettleKey(table, index), m.opts.Clock.Now(), statusActive)
}

// ListIndexes returns all Global Secondary Indexes for a table.
func (m *Mock) ListIndexes(_ context.Context, table string) ([]driver.IndexInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	now := m.opts.Clock.Now()
	indexes := make([]driver.IndexInfo, 0, len(td.config.GSIs))

	for _, gsi := range td.config.GSIs {
		indexes = append(indexes, driver.IndexInfo{
			Name:         gsi.Name,
			PartitionKey: gsi.PartitionKey,
			SortKey:      gsi.SortKey,
			Status:       m.gsiSettle.State(gsiSettleKey(table, gsi.Name), now, statusActive),
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
