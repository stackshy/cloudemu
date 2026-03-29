// Package firestore provides an in-memory mock implementation of Google Cloud Firestore.
package firestore

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

// Snapshot and TTL constants.
const (
	ViewNewImage       = "NEW_IMAGE"
	ViewOldImage       = "OLD_IMAGE"
	ViewNewAndOld      = "NEW_AND_OLD_IMAGES"
	ViewKeysOnly       = "KEYS_ONLY"
	maxSnapshotRecords = 1000
	defaultSnapLimit   = 100
)

// Compile-time check that Mock implements driver.Database.
var _ driver.Database = (*Mock)(nil)

type collectionData struct {
	config        driver.TableConfig
	items         *memstore.Store[map[string]any]
	ttlConfig     driver.TTLConfig
	streamConfig  driver.StreamConfig
	streamRecords []driver.StreamRecord
	seqCounter    atomic.Int64
}

// Mock is an in-memory mock implementation of Google Cloud Firestore.
type Mock struct {
	mu          sync.RWMutex
	collections map[string]*collectionData
	opts        *config.Options
	monitoring  mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(ctx context.Context, metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(ctx, []mondriver.MetricDatum{
		{
			Namespace:  "firestore.googleapis.com",
			MetricName: metricName,
			Value:      value,
			Unit:       "None",
			Dimensions: dims,
			Timestamp:  m.opts.Clock.Now(),
		},
	})
}

// New creates a new Firestore mock.
func New(opts *config.Options) *Mock {
	return &Mock{collections: make(map[string]*collectionData), opts: opts}
}

func docKey(cfg driver.TableConfig, item map[string]any) string {
	pk := fmt.Sprintf("%v", item[cfg.PartitionKey])
	if cfg.SortKey != "" {
		return pk + "/" + fmt.Sprintf("%v", item[cfg.SortKey])
	}

	return pk
}

func (m *Mock) CreateTable(_ context.Context, cfg driver.TableConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.collections[cfg.Name]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "collection %s already exists", cfg.Name)
	}

	m.collections[cfg.Name] = &collectionData{config: cfg, items: memstore.New[map[string]any]()}

	return nil
}

func (m *Mock) DeleteTable(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.collections[name]; !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", name)
	}

	delete(m.collections, name)

	return nil
}

func (m *Mock) DescribeTable(_ context.Context, name string) (*driver.TableConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cd, exists := m.collections[name]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", name)
	}

	cfg := cd.config

	return &cfg, nil
}

func (m *Mock) ListTables(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.collections))
	for name := range m.collections {
		names = append(names, name)
	}

	return names, nil
}

func (m *Mock) PutItem(ctx context.Context, table string, item map[string]any) error {
	m.mu.Lock()

	cd, exists := m.collections[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	key := docKey(cd.config, item)
	oldItem, hadOld := cd.items.Get(key)
	cd.items.Set(key, item)
	m.recordStreamEvent(cd, oldItem, item, hadOld)
	m.mu.Unlock()

	m.emitMetric(ctx, "document/write_count", 1, map[string]string{"collection_id": table})

	return nil
}

func (m *Mock) GetItem(ctx context.Context, table string, key map[string]any) (map[string]any, error) {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	k := docKey(cd.config, key)
	item, ok := cd.items.Get(k)

	if !ok {
		return nil, cerrors.New(cerrors.NotFound, "document not found")
	}

	if m.isItemExpired(cd, item) {
		cd.items.Delete(k)
		return nil, cerrors.New(cerrors.NotFound, "document not found")
	}

	m.emitMetric(ctx, "document/read_count", 1, map[string]string{"collection_id": table})

	return item, nil
}

// UpdateItem applies partial updates to an existing document in a collection.
func (m *Mock) UpdateItem(ctx context.Context, input driver.UpdateItemInput) (map[string]any, error) {
	m.mu.Lock()

	cd, exists := m.collections[input.Table]
	if !exists {
		m.mu.Unlock()
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", input.Table)
	}

	k := docKey(cd.config, input.Key)
	item, ok := cd.items.Get(k)

	if !ok {
		m.mu.Unlock()
		return nil, cerrors.New(cerrors.NotFound, "document not found")
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

	cd.items.Set(k, updated)
	m.recordStreamEvent(cd, oldItem, updated, true)
	m.mu.Unlock()

	m.emitMetric(ctx, "document/write_count", 1, map[string]string{"collection_id": input.Table})

	return updated, nil
}

func (m *Mock) DeleteItem(ctx context.Context, table string, key map[string]any) error {
	m.mu.Lock()

	cd, exists := m.collections[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	k := docKey(cd.config, key)
	oldItem, hadOld := cd.items.Get(k)
	cd.items.Delete(k)

	if hadOld {
		m.recordStreamRemove(cd, oldItem)
	}

	m.mu.Unlock()

	m.emitMetric(ctx, "document/delete_count", 1, map[string]string{"collection_id": table})

	return nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Query(ctx context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	cd, exists := m.collections[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", input.Table)
	}

	pkField, skField, err := resolveKeyFields(cd, input.IndexName)
	if err != nil {
		return nil, err
	}

	matched := m.matchQueryItems(cd, pkField, skField, &input)

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	page, _ := pagination.Paginate(matched, input.PageToken, limit)

	m.emitMetric(ctx, "document/read_count", float64(len(page.Items)), map[string]string{"collection_id": input.Table})

	return &driver.QueryResult{Items: page.Items, Count: len(page.Items), NextPageToken: page.NextPageToken}, nil
}

func resolveKeyFields(cd *collectionData, indexName string) (pkField, skField string, err error) {
	pkField = cd.config.PartitionKey
	skField = cd.config.SortKey

	if indexName == "" {
		return pkField, skField, nil
	}

	for _, gsi := range cd.config.GSIs {
		if gsi.Name == indexName {
			return gsi.PartitionKey, gsi.SortKey, nil
		}
	}

	return "", "", cerrors.Newf(cerrors.NotFound, "index %s not found", indexName)
}

func (m *Mock) matchQueryItems(
	cd *collectionData, pkField, skField string, input *driver.QueryInput,
) []map[string]any {
	allItems := cd.items.All()

	var matched []map[string]any

	for _, item := range allItems {
		if m.isItemExpired(cd, item) {
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

func (m *Mock) Scan(ctx context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	cd, exists := m.collections[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", input.Table)
	}

	allItems := cd.items.All()

	var matched []map[string]any

	for _, item := range allItems {
		if m.isItemExpired(cd, item) {
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

	m.emitMetric(ctx, "document/read_count", float64(len(page.Items)), map[string]string{"collection_id": input.Table})

	return &driver.QueryResult{Items: page.Items, Count: len(page.Items), NextPageToken: page.NextPageToken}, nil
}

func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]any) error {
	m.mu.Lock()

	cd, exists := m.collections[table]
	if !exists {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	for _, item := range items {
		key := docKey(cd.config, item)
		oldItem, hadOld := cd.items.Get(key)
		cd.items.Set(key, item)
		m.recordStreamEvent(cd, oldItem, item, hadOld)
	}

	m.mu.Unlock()

	return nil
}

func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]any) ([]map[string]any, error) {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	var results []map[string]any

	for _, key := range keys {
		if item, ok := cd.items.Get(docKey(cd.config, key)); ok {
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

// UpdateTTL configures TTL for a collection.
func (m *Mock) UpdateTTL(_ context.Context, table string, cfg driver.TTLConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cd, exists := m.collections[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	cd.ttlConfig = cfg

	return nil
}

// DescribeTTL returns the TTL configuration for a collection.
func (m *Mock) DescribeTTL(_ context.Context, table string) (*driver.TTLConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cd, exists := m.collections[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	cfg := cd.ttlConfig

	return &cfg, nil
}

// UpdateStreamConfig configures real-time listeners for a collection.
func (m *Mock) UpdateStreamConfig(_ context.Context, table string, cfg driver.StreamConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cd, exists := m.collections[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	cd.streamConfig = cfg

	return nil
}

// GetStreamRecords returns snapshot records after the given token.
func (m *Mock) GetStreamRecords(
	_ context.Context, table string, limit int, token string,
) (*driver.StreamIterator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cd, exists := m.collections[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	if !cd.streamConfig.Enabled {
		return nil, cerrors.New(cerrors.FailedPrecondition, "listeners not enabled")
	}

	return filterSnapRecords(cd.streamRecords, limit, token), nil
}

func filterSnapRecords(records []driver.StreamRecord, limit int, token string) *driver.StreamIterator {
	if limit <= 0 {
		limit = defaultSnapLimit
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
		ShardID:   "snapshot-000",
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

	cd, exists := m.collections[table]
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	m.applyTransactPuts(cd, puts)
	m.applyTransactDeletes(cd, deletes)

	return nil
}

func (m *Mock) applyTransactPuts(cd *collectionData, puts []map[string]any) {
	for _, item := range puts {
		key := docKey(cd.config, item)
		oldItem, hadOld := cd.items.Get(key)
		cd.items.Set(key, item)
		m.recordStreamEvent(cd, oldItem, item, hadOld)
	}
}

func (m *Mock) applyTransactDeletes(cd *collectionData, deletes []map[string]any) {
	for _, key := range deletes {
		k := docKey(cd.config, key)
		oldItem, hadOld := cd.items.Get(k)
		cd.items.Delete(k)

		if hadOld {
			m.recordStreamRemove(cd, oldItem)
		}
	}
}

// isItemExpired checks if a document has expired based on TTL config.
func (m *Mock) isItemExpired(cd *collectionData, item map[string]any) bool {
	if !cd.ttlConfig.Enabled {
		return false
	}

	ttlVal, ok := item[cd.ttlConfig.AttributeName]
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

// recordStreamEvent records an INSERT or MODIFY snapshot event. Caller must hold m.mu.
func (m *Mock) recordStreamEvent(cd *collectionData, oldItem, newItem map[string]any, hadOld bool) {
	if !cd.streamConfig.Enabled {
		return
	}

	eventType := "INSERT"
	if hadOld {
		eventType = "MODIFY"
	}

	rec := m.buildStreamRecord(cd, eventType, oldItem, newItem)
	cd.streamRecords = appendSnapRecord(cd.streamRecords, &rec)
}

// recordStreamRemove records a REMOVE snapshot event. Caller must hold m.mu.
func (m *Mock) recordStreamRemove(cd *collectionData, oldItem map[string]any) {
	if !cd.streamConfig.Enabled {
		return
	}

	rec := m.buildStreamRecord(cd, "REMOVE", oldItem, nil)
	cd.streamRecords = appendSnapRecord(cd.streamRecords, &rec)
}

func (m *Mock) buildStreamRecord(
	cd *collectionData, eventType string, oldItem, newItem map[string]any,
) driver.StreamRecord {
	seq := cd.seqCounter.Add(1)
	keys := extractKeys(cd.config, oldItem, newItem)

	rec := driver.StreamRecord{
		EventID:        fmt.Sprintf("event-%d", seq),
		EventType:      eventType,
		Table:          cd.config.Name,
		Keys:           keys,
		Timestamp:      m.opts.Clock.Now(),
		SequenceNumber: fmt.Sprintf("%d", seq),
	}

	applyViewType(&rec, cd.streamConfig.ViewType, oldItem, newItem)

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

func appendSnapRecord(records []driver.StreamRecord, rec *driver.StreamRecord) []driver.StreamRecord {
	records = append(records, *rec)
	if len(records) > maxSnapshotRecords {
		records = records[len(records)-maxSnapshotRecords:]
	}

	return records
}
