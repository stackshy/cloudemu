// Package cosmosdb provides an in-memory mock implementation of Azure Cosmos DB.
package cosmosdb

import (
	"context"
	"fmt"
	"strconv"
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

type tableData struct {
	config driver.TableConfig
	items  *memstore.Store[map[string]interface{}]
}

// Mock is an in-memory mock implementation of Azure Cosmos DB.
type Mock struct {
	mu     sync.RWMutex
	tables map[string]*tableData
	opts   *config.Options
}

// New creates a new Cosmos DB mock.
func New(opts *config.Options) *Mock {
	return &Mock{tables: make(map[string]*tableData), opts: opts}
}

func itemKey(cfg driver.TableConfig, item map[string]interface{}) string {
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
	m.tables[cfg.Name] = &tableData{config: cfg, items: memstore.New[map[string]interface{}]()}
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
func (m *Mock) PutItem(_ context.Context, table string, item map[string]interface{}) error {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}
	td.items.Set(itemKey(td.config, item), item)
	return nil
}

// GetItem retrieves an item from a container by key.
func (m *Mock) GetItem(_ context.Context, table string, key map[string]interface{}) (map[string]interface{}, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}
	item, ok := td.items.Get(itemKey(td.config, key))
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, "item not found")
	}
	return item, nil
}

// DeleteItem deletes an item from a container by key.
func (m *Mock) DeleteItem(_ context.Context, table string, key map[string]interface{}) error {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}
	td.items.Delete(itemKey(td.config, key))
	return nil
}

// Query executes a query against a container.
func (m *Mock) Query(_ context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	td, exists := m.tables[input.Table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", input.Table)
	}

	pkField := td.config.PartitionKey
	skField := td.config.SortKey
	if input.IndexName != "" {
		found := false
		for _, gsi := range td.config.GSIs {
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

	allItems := td.items.All()
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

// Scan scans all items in a container with optional filters.
func (m *Mock) Scan(_ context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()
	td, exists := m.tables[input.Table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", input.Table)
	}
	allItems := td.items.All()
	var matched []map[string]interface{}
	for _, item := range allItems {
		if matchesScanFilters(item, input.Filters) {
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

// BatchPutItems stores multiple items in a container.
func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]interface{}) error {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()
	if !exists {
		return cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}
	for _, item := range items {
		td.items.Set(itemKey(td.config, item), item)
	}
	return nil
}

// BatchGetItems retrieves multiple items from a container by keys.
func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]interface{}) ([]map[string]interface{}, error) {
	m.mu.RLock()
	td, exists := m.tables[table]
	m.mu.RUnlock()
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "container %s not found", table)
	}
	var results []map[string]interface{}
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

func applySortCondition(itemVal, op, condVal string, condValEnd interface{}) bool {
	switch op {
	case "=":
		return itemVal == condVal
	case "<":
		return compareValues(itemVal, condVal) < 0
	case ">":
		return compareValues(itemVal, condVal) > 0
	case "<=":
		return compareValues(itemVal, condVal) <= 0
	case ">=":
		return compareValues(itemVal, condVal) >= 0
	case "BEGINS_WITH":
		return strings.HasPrefix(itemVal, condVal)
	case "BETWEEN":
		endVal := fmt.Sprintf("%v", condValEnd)
		return compareValues(itemVal, condVal) >= 0 && compareValues(itemVal, endVal) <= 0
	default:
		return false
	}
}

func matchesScanFilters(item map[string]interface{}, filters []driver.ScanFilter) bool {
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
			if compareValues(val, condVal) >= 0 {
				return false
			}
		case ">":
			if compareValues(val, condVal) <= 0 {
				return false
			}
		case "<=":
			if compareValues(val, condVal) > 0 {
				return false
			}
		case ">=":
			if compareValues(val, condVal) < 0 {
				return false
			}
		case "CONTAINS":
			if !strings.Contains(val, condVal) {
				return false
			}
		case "BEGINS_WITH":
			if !strings.HasPrefix(val, condVal) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
