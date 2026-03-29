// Package cosmosdb provides an in-memory mock implementation of Azure Cosmos DB.
package cosmosdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/pagination"
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

// Change feed and TTL constants.
const (
	ViewNewImage     = "NEW_IMAGE"
	ViewOldImage     = "OLD_IMAGE"
	ViewNewAndOld    = "NEW_AND_OLD_IMAGES"
	ViewKeysOnly     = "KEYS_ONLY"
	maxFeedRecords   = 1000
	defaultFeedLimit = 100
)

// Compile-time check that Mock implements driver.Database.
var _ driver.Database = (*Mock)(nil)

type tableData struct {
	config        driver.TableConfig
	items         *memstore.Store[map[string]any]
	ttlConfig     driver.TTLConfig
	streamConfig  driver.StreamConfig
	streamRecords []driver.StreamRecord
	seqCounter    atomic.Int64
}

// Mock is an in-memory mock implementation of Azure Cosmos DB.
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

func (m *Mock) emitMetric(container string, metrics map[string]float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, 0, len(metrics))

	for name, value := range metrics {
		data = append(data, mondriver.MetricDatum{
			Namespace:  "Microsoft.DocumentDB/databaseAccounts",
			MetricName: name,
			Value:      value,
			Unit:       "None",
			Dimensions: map[string]string{"containerName": container},
			Timestamp:  now,
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// New creates a new Cosmos DB mock.
func New(opts *config.Options) *Mock {
	return &Mock{tables: make(map[string]*tableData), opts: opts}
}

func itemKey(cfg driver.TableConfig, item map[string]any) string {
	pk := fmt.Sprintf("%v", item[cfg.PartitionKey])
	if cfg.SortKey != "" {
		return pk + ":" + fmt.Sprintf("%v", item[cfg.SortKey])
	}

	return pk
}

// CreateTable creates a new container (table) in Cosmos DB.
func (m *Mock) CreateTable(_ context.Context, cfg driver.TableConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tables[cfg.Name]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "container %s already exists", cfg.Name)
	}

	m.tables[cfg.Name] = &tableData{config: cfg, items: memstore.New[map[string]any]()}

	return nil
}

// DeleteTable deletes a container (table) from Cosmos DB.
func (m *Mock) DeleteTable(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tables[name]; !exists {
		return cerrors.Newf(cerrors.NotFound, "container %s not found", name)
	}

	delete(m.tables, name)

	return nil
}

// DescribeTable returns the configuration of a container (table).
func (m *Mock) DescribeTable(_ context.Context, name string) (*driver.TableConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[name]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", name)
	}

	cfg := td.config

	return &cfg, nil
}

// ListTables lists all containers (tables) in Cosmos DB.
func (m *Mock) ListTables(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.tables))

	for name := range m.tables {
		names = append(names, name)
	}

	return names, nil
}

// PutItem stores an item in a container.
func (m *Mock) PutItem(_ context.Context, table string, item map[string]any) error {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	key := itemKey(td.config, item)
	oldItem, hadOld := td.items.Get(key)
	td.items.Set(key, item)
	m.recordStreamEvent(td, oldItem, item, hadOld)
	m.mu.Unlock()

	m.emitMetric(table, map[string]float64{"TotalRequests": 1, "TotalRequestUnits": 1})

	return nil
}

// GetItem retrieves an item from a container by key.
func (m *Mock) GetItem(_ context.Context, table string, key map[string]any) (map[string]any, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", table)
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

	m.emitMetric(table, map[string]float64{"TotalRequests": 1, "TotalRequestUnits": 1})

	return item, nil
}

// UpdateItem applies partial updates to an existing document in a container.
func (m *Mock) UpdateItem(_ context.Context, input driver.UpdateItemInput) (map[string]any, error) {
	m.mu.Lock()

	td, exists := m.tables[input.Table]
	if !exists {
		m.mu.Unlock()
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", input.Table)
	}

	k := itemKey(td.config, input.Key)
	item, ok := td.items.Get(k)

	if !ok {
		m.mu.Unlock()
		return nil, cerrors.New(cerrors.NotFound, "item not found")
	}

	oldItem := copyItem(item)
	updated := copyItem(item)

	for _, action := range input.Actions {
		switch action.Action {
		case "SET":
			updated[action.Field] = action.Value
		case "REMOVE":
			delete(updated, action.Field)
		default:
			m.mu.Unlock()
			return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported action: %s", action.Action)
		}
	}

	td.items.Set(k, updated)
	m.recordStreamEvent(td, oldItem, updated, true)
	m.mu.Unlock()

	m.emitMetric(input.Table, map[string]float64{"TotalRequests": 1, "TotalRequestUnits": 1})

	return updated, nil
}

// DeleteItem deletes an item from a container by key.
func (m *Mock) DeleteItem(_ context.Context, table string, key map[string]any) error {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	k := itemKey(td.config, key)
	oldItem, hadOld := td.items.Get(k)
	td.items.Delete(k)

	if hadOld {
		m.recordStreamRemove(td, oldItem)
	}

	m.mu.Unlock()

	m.emitMetric(table, map[string]float64{"TotalRequests": 1, "TotalRequestUnits": 1})

	return nil
}

// Query executes a query against a container.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Query(_ context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	td, exists := m.tables[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", input.Table)
	}

	pkField, skField, err := resolveKeyFields(td, input.IndexName)
	if err != nil {
		return nil, err
	}

	matched := m.matchQueryItems(td, pkField, skField, &input)

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	page, _ := pagination.Paginate(matched, input.PageToken, limit)

	m.emitMetric(input.Table, map[string]float64{"TotalRequests": 1, "TotalRequestUnits": float64(len(page.Items))})

	return &driver.QueryResult{Items: page.Items, Count: len(page.Items), NextPageToken: page.NextPageToken}, nil
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

	return "", "", cerrors.Newf(cerrors.NotFound, "index %s not found", indexName)
}

// Scan scans all items in a container with optional filters.
func (m *Mock) Scan(_ context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	td, exists := m.tables[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", input.Table)
	}

	allItems := td.items.All()

	var matched []map[string]any

	for _, item := range allItems {
		if m.isItemExpired(td, item) {
			continue
		}

		if matchesFilters(item, input.Filters) {
			matched = append(matched, item)
		}
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	page, _ := pagination.Paginate(matched, input.PageToken, limit)

	m.emitMetric(input.Table, map[string]float64{"TotalRequests": 1, "TotalRequestUnits": float64(len(page.Items))})

	return &driver.QueryResult{Items: page.Items, Count: len(page.Items), NextPageToken: page.NextPageToken}, nil
}

// BatchPutItems stores multiple items in a container.
func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]any) error {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	for _, item := range items {
		key := itemKey(td.config, item)
		oldItem, hadOld := td.items.Get(key)
		td.items.Set(key, item)
		m.recordStreamEvent(td, oldItem, item, hadOld)
	}

	m.mu.Unlock()

	return nil
}

// BatchGetItems retrieves multiple items from a container by keys.
func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]any) ([]map[string]any, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	var results []map[string]any

	for _, key := range keys {
		if item, ok := td.items.Get(itemKey(td.config, key)); ok {
			results = append(results, item)
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

func (m *Mock) matchQueryItems(
	td *tableData, pkField, skField string, input *driver.QueryInput,
) []map[string]any {
	allItems := td.items.All()

	var matched []map[string]any

	for _, item := range allItems {
		if m.isItemExpired(td, item) {
			continue
		}

		pkVal := fmt.Sprintf("%v", item[pkField])
		if pkVal != fmt.Sprintf("%v", input.KeyCondition.PartitionVal) {
			continue
		}

		if input.KeyCondition.SortOp != "" && skField != "" {
			skVal := fmt.Sprintf("%v", item[skField])
			condSK := fmt.Sprintf("%v", input.KeyCondition.SortVal)

			if !applySortCondition(skVal, input.KeyCondition.SortOp, condSK, input.KeyCondition.SortValEnd) {
				continue
			}
		}

		matched = append(matched, item)
	}

	return matched
}

// UpdateTTL configures TTL for a container.
func (m *Mock) UpdateTTL(_ context.Context, table string, cfg driver.TTLConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	td.ttlConfig = cfg

	return nil
}

// DescribeTTL returns the TTL configuration for a container.
func (m *Mock) DescribeTTL(_ context.Context, table string) (*driver.TTLConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	cfg := td.ttlConfig

	return &cfg, nil
}

// UpdateStreamConfig configures the change feed for a container.
func (m *Mock) UpdateStreamConfig(_ context.Context, table string, cfg driver.StreamConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	td.streamConfig = cfg

	return nil
}

// GetStreamRecords returns change feed records after the given token.
func (m *Mock) GetStreamRecords(
	_ context.Context, table string, limit int, token string,
) (*driver.StreamIterator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	if !td.streamConfig.Enabled {
		return nil, cerrors.New(cerrors.FailedPrecondition, "change feed not enabled")
	}

	return filterFeedRecords(td.streamRecords, limit, token), nil
}

func filterFeedRecords(records []driver.StreamRecord, limit int, token string) *driver.StreamIterator {
	if limit <= 0 {
		limit = defaultFeedLimit
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
		ShardID:   "lease-000",
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
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}

	m.applyTransactPuts(td, puts)
	m.applyTransactDeletes(td, deletes)

	return nil
}

func (m *Mock) applyTransactPuts(td *tableData, puts []map[string]any) {
	for _, item := range puts {
		key := itemKey(td.config, item)
		oldItem, hadOld := td.items.Get(key)
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
// CosmosDB uses TTL in seconds relative to a _ts field or creation time.
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

// recordStreamEvent records an INSERT or MODIFY change feed event. Caller must hold m.mu.
func (m *Mock) recordStreamEvent(td *tableData, oldItem, newItem map[string]any, hadOld bool) {
	if !td.streamConfig.Enabled {
		return
	}

	eventType := "INSERT"
	if hadOld {
		eventType = "MODIFY"
	}

	rec := m.buildStreamRecord(td, eventType, oldItem, newItem)
	td.streamRecords = appendFeedRecord(td.streamRecords, &rec)
}

// recordStreamRemove records a REMOVE change feed event. Caller must hold m.mu.
func (m *Mock) recordStreamRemove(td *tableData, oldItem map[string]any) {
	if !td.streamConfig.Enabled {
		return
	}

	rec := m.buildStreamRecord(td, "REMOVE", oldItem, nil)
	td.streamRecords = appendFeedRecord(td.streamRecords, &rec)
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

func appendFeedRecord(records []driver.StreamRecord, rec *driver.StreamRecord) []driver.StreamRecord {
	records = append(records, *rec)
	if len(records) > maxFeedRecords {
		records = records[len(records)-maxFeedRecords:]
	}

	return records
}
