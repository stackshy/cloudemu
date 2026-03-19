// Package firestore provides an in-memory mock implementation of Google Cloud Firestore.
package firestore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/database/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
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

// Compile-time check that Mock implements driver.Database.
var _ driver.Database = (*Mock)(nil)

type collectionData struct {
	config driver.TableConfig
	items  *memstore.Store[map[string]any]
}

// Mock is an in-memory mock implementation of Google Cloud Firestore.
type Mock struct {
	mu          sync.RWMutex
	collections map[string]*collectionData
	opts        *config.Options
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

func (m *Mock) PutItem(_ context.Context, table string, item map[string]any) error {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()

	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	cd.items.Set(docKey(cd.config, item), item)

	return nil
}

func (m *Mock) GetItem(_ context.Context, table string, key map[string]any) (map[string]any, error) {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	item, ok := cd.items.Get(docKey(cd.config, key))
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, "document not found")
	}

	return item, nil
}

func (m *Mock) DeleteItem(_ context.Context, table string, key map[string]any) error {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()

	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	cd.items.Delete(docKey(cd.config, key))

	return nil
}

//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Query(_ context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
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

	matched := matchQueryItems(cd, pkField, skField, &input)

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	page, _ := pagination.Paginate(matched, input.PageToken, limit)

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

func matchQueryItems(cd *collectionData, pkField, skField string, input *driver.QueryInput) []map[string]any {
	allItems := cd.items.All()

	var matched []map[string]any

	for _, item := range allItems {
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

func (m *Mock) Scan(_ context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	cd, exists := m.collections[input.Table]
	m.mu.RUnlock()

	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", input.Table)
	}

	allItems := cd.items.All()

	var matched []map[string]any

	for _, item := range allItems {
		if matchesFilters(item, input.Filters) {
			matched = append(matched, item)
		}
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	page, _ := pagination.Paginate(matched, input.PageToken, limit)

	return &driver.QueryResult{Items: page.Items, Count: len(page.Items), NextPageToken: page.NextPageToken}, nil
}

func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]any) error {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()

	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}

	for _, item := range items {
		cd.items.Set(docKey(cd.config, item), item)
	}

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
