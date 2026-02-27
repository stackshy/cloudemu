// Package firestore provides an in-memory mock implementation of Google Cloud Firestore.
package firestore

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/NitinKumar004/cloudemu/config"
	"github.com/NitinKumar004/cloudemu/database/driver"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
	"github.com/NitinKumar004/cloudemu/pagination"
)

// Compile-time check that Mock implements driver.Database.
var _ driver.Database = (*Mock)(nil)

type collectionData struct {
	config driver.TableConfig
	items  *memstore.Store[map[string]interface{}]
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

func docKey(cfg driver.TableConfig, item map[string]interface{}) string {
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
	m.collections[cfg.Name] = &collectionData{config: cfg, items: memstore.New[map[string]interface{}]()}
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

func (m *Mock) PutItem(_ context.Context, table string, item map[string]interface{}) error {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}
	cd.items.Set(docKey(cd.config, item), item)
	return nil
}

func (m *Mock) GetItem(_ context.Context, table string, key map[string]interface{}) (map[string]interface{}, error) {
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

func (m *Mock) DeleteItem(_ context.Context, table string, key map[string]interface{}) error {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}
	cd.items.Delete(docKey(cd.config, key))
	return nil
}

func (m *Mock) Query(_ context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	cd, exists := m.collections[input.Table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", input.Table)
	}

	pkField := cd.config.PartitionKey
	skField := cd.config.SortKey
	if input.IndexName != "" {
		found := false
		for _, gsi := range cd.config.GSIs {
			if gsi.Name == input.IndexName {
				pkField = gsi.PartitionKey
				skField = gsi.SortKey
				found = true
				break
			}
		}
		if !found {
			return nil, cerrors.Newf(cerrors.NotFound, "index %s not found", input.IndexName)
		}
	}

	allItems := cd.items.All()
	var matched []map[string]interface{}
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

	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}
	page, _ := pagination.Paginate(matched, input.PageToken, limit)
	return &driver.QueryResult{Items: page.Items, Count: len(page.Items), NextPageToken: page.NextPageToken}, nil
}

func (m *Mock) Scan(_ context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	cd, exists := m.collections[input.Table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", input.Table)
	}
	allItems := cd.items.All()
	var matched []map[string]interface{}
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

func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]interface{}) error {
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

func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]interface{}) ([]map[string]interface{}, error) {
	m.mu.RLock()
	cd, exists := m.collections[table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "collection %s not found", table)
	}
	var results []map[string]interface{}
	for _, key := range keys {
		if item, ok := cd.items.Get(docKey(cd.config, key)); ok {
			results = append(results, item)
		}
	}
	return results, nil
}

func applySortCondition(itemVal, op, condVal string, condValEnd interface{}) bool {
	switch op {
	case "=":
		return itemVal == condVal
	case "<":
		return itemVal < condVal
	case ">":
		return itemVal > condVal
	case "<=":
		return itemVal <= condVal
	case ">=":
		return itemVal >= condVal
	case "BEGINS_WITH":
		return strings.HasPrefix(itemVal, condVal)
	case "BETWEEN":
		return itemVal >= condVal && itemVal <= fmt.Sprintf("%v", condValEnd)
	default:
		return false
	}
}

func matchesFilters(item map[string]interface{}, filters []driver.ScanFilter) bool {
	for _, f := range filters {
		val := fmt.Sprintf("%v", item[f.Field])
		condVal := fmt.Sprintf("%v", f.Value)
		switch f.Op {
		case "=":
			if val != condVal {
				return false
			}
		case "!=":
			if val == condVal {
				return false
			}
		case "<":
			if val >= condVal {
				return false
			}
		case ">":
			if val <= condVal {
				return false
			}
		case "CONTAINS":
			if !strings.Contains(val, condVal) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
