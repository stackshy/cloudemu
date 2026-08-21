package nosql

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// Filter and key-condition operators.
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

// PutItem writes a row, replacing any row with the same primary key.
func (m *Mock) PutItem(_ context.Context, table string, item map[string]any) error {
	m.mu.Lock()

	t, err := m.lookup(table)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	m.putRow(t, item)
	m.mu.Unlock()

	m.emitMetric("WriteUnits", 1, table)

	return nil
}

// putRow stores a row and stamps the table-level TTL expiry on it. Callers
// must hold m.mu.
func (m *Mock) putRow(t *tableData, item map[string]any) map[string]any {
	stored := maps.Clone(item)
	delete(stored, ttlExpiryColumn)

	if exp := m.expiryOf(t); exp > 0 {
		stored[ttlExpiryColumn] = exp
	}

	t.items.Set(itemKey(t, stored), stored)

	return stored
}

// expiryOf returns the Unix time a row written now expires at, or zero when
// the table declares no TTL.
func (m *Mock) expiryOf(t *tableData) int64 {
	if t.Schema.TTL.Days <= 0 {
		return 0
	}

	const hoursPerDay = 24

	return m.opts.Clock.Now().Add(time.Duration(t.Schema.TTL.Days*hoursPerDay) * time.Hour).Unix()
}

// GetItem returns a row by primary key.
func (m *Mock) GetItem(_ context.Context, table string, key map[string]any) (map[string]any, error) {
	m.mu.RLock()

	t, err := m.lookup(table)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	item, ok := t.items.Get(itemKey(t, key))
	expired := ok && m.expired(t, item)

	if ok && !expired {
		item = visible(item)
	}

	m.mu.RUnlock()

	if !ok || expired {
		return nil, cerrors.New(cerrors.NotFound, "row not found")
	}

	m.emitMetric("ReadUnits", 1, table)

	return item, nil
}

// UpdateItem applies field-level updates to an existing row.
func (m *Mock) UpdateItem(_ context.Context, input driver.UpdateItemInput) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(input.Table)
	if err != nil {
		return nil, err
	}

	k := itemKey(t, input.Key)

	item, ok := t.items.Get(k)
	if !ok || m.expired(t, item) {
		return nil, cerrors.New(cerrors.NotFound, "row not found")
	}

	updated := maps.Clone(item)

	for _, a := range input.Actions {
		switch a.Action {
		case "SET":
			updated[a.Field] = a.Value
		case "REMOVE":
			delete(updated, a.Field)
		default:
			return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported update action %q", a.Action)
		}
	}

	t.items.Set(k, updated)

	return visible(updated), nil
}

// DeleteItem removes a row by primary key. Deleting a row that is not there
// is not an error, matching OCI's DeleteRow.
func (m *Mock) DeleteItem(_ context.Context, table string, key map[string]any) error {
	m.mu.Lock()

	t, err := m.lookup(table)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	t.items.Delete(itemKey(t, key))
	m.mu.Unlock()

	m.emitMetric("WriteUnits", 1, table)

	return nil
}

// Query returns the rows sharing a shard key value, optionally narrowed by a
// sort key condition and post-conditions.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) Query(_ context.Context, input driver.QueryInput) (*driver.QueryResult, error) {
	m.mu.RLock()

	t, err := m.lookup(input.Table)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	pkField, skField, err := indexKeys(t, input.IndexName)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	var matched []map[string]any

	for _, item := range m.liveItems(t) {
		if !matchesKeyCondition(item, pkField, skField, &input.KeyCondition) {
			continue
		}

		if matchesFilters(item, input.Filters) {
			matched = append(matched, item)
		}
	}

	keyFields := []string{t.Schema.ShardKey[0], sortKeyOf(&t.Schema)}
	if input.IndexName != "" {
		keyFields = append(keyFields, pkField, skField)
	}

	identity := identityFunc(t)
	m.mu.RUnlock()

	res, err := driver.PageOrdered(matched, pkField, skField, keyFields,
		pageLimit(input.Limit), input.PageToken, input.ExclusiveStartKey, input.SortDescending, identity)
	if err != nil {
		return nil, err
	}

	m.emitMetric("ReadUnits", float64(len(res.Items)), input.Table)

	return res, nil
}

// Scan returns every row, narrowed by filters.
func (m *Mock) Scan(_ context.Context, input driver.ScanInput) (*driver.QueryResult, error) {
	m.mu.RLock()

	t, err := m.lookup(input.Table)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	var matched []map[string]any

	for _, item := range m.liveItems(t) {
		if matchesFilters(item, input.Filters) {
			matched = append(matched, item)
		}
	}

	pk, sk := t.Schema.ShardKey[0], sortKeyOf(&t.Schema)
	identity := identityFunc(t)
	m.mu.RUnlock()

	res, err := driver.PageOrdered(matched, pk, sk, []string{pk, sk},
		pageLimit(input.Limit), input.PageToken, input.ExclusiveStartKey, false, identity)
	if err != nil {
		return nil, err
	}

	m.emitMetric("ReadUnits", float64(len(res.Items)), input.Table)

	return res, nil
}

// identityFunc returns a row-identity function safe to call after m.mu is
// released: it closes over the key columns rather than the table pointer.
func identityFunc(t *tableData) func(map[string]any) string {
	pk, sk := t.Schema.ShardKey[0], sortKeyOf(&t.Schema)

	return func(item map[string]any) string {
		key := fmt.Sprintf("%v", item[pk])
		if sk != "" {
			key += ":" + fmt.Sprintf("%v", item[sk])
		}

		return key
	}
}

// indexKeys resolves the columns a query orders and filters on. Callers must
// hold m.mu.
func indexKeys(t *tableData, indexName string) (pkField, skField string, err error) {
	if indexName == "" {
		return t.Schema.ShardKey[0], sortKeyOf(&t.Schema), nil
	}

	for _, idx := range t.Indexes {
		if idx.Name != indexName {
			continue
		}

		cfg := toGSIConfig(idx)

		return cfg.PartitionKey, cfg.SortKey, nil
	}

	return "", "", cerrors.Newf(cerrors.NotFound, "index %q not found on table %q", indexName, t.Name)
}

func pageLimit(limit int) int {
	if limit <= 0 {
		return defaultPageLimit
	}

	return limit
}

func matchesKeyCondition(item map[string]any, pkField, skField string, cond *driver.KeyCondition) bool {
	if fmt.Sprintf("%v", item[pkField]) != fmt.Sprintf("%v", cond.PartitionVal) {
		return false
	}

	if cond.SortOp == "" || skField == "" {
		return true
	}

	return compareOp(fmt.Sprintf("%v", item[skField]), cond.SortOp,
		fmt.Sprintf("%v", cond.SortVal), fmt.Sprintf("%v", cond.SortValEnd))
}

func matchesFilters(item map[string]any, filters []driver.ScanFilter) bool {
	for _, f := range filters {
		if !compareOp(fmt.Sprintf("%v", item[f.Field]), f.Op, fmt.Sprintf("%v", f.Value), "") {
			return false
		}
	}

	return true
}

// compareOp applies one comparison, ordering numerically when both sides parse
// as numbers and lexically otherwise.
func compareOp(val, op, want, end string) bool {
	switch op {
	case OpEqual:
		return val == want
	case OpNotEqual:
		return val != want
	case OpContains:
		return strings.Contains(val, want)
	case OpBeginsWith:
		return strings.HasPrefix(val, want)
	case OpBetween:
		return compareStrings(val, want) >= 0 && compareStrings(val, end) <= 0
	}

	return compareOrdering(val, op, want)
}

// compareOrdering applies the four relational operators.
func compareOrdering(val, op, want string) bool {
	c := compareStrings(val, want)

	switch op {
	case OpLessThan:
		return c < 0
	case OpGreaterThan:
		return c > 0
	case OpLessEqual:
		return c <= 0
	case OpGreaterEqual:
		return c >= 0
	}

	return false
}

func compareStrings(a, b string) int {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)

	if errA == nil && errB == nil {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}

		return 0
	}

	return strings.Compare(a, b)
}

// BatchPutItems writes several rows.
func (m *Mock) BatchPutItems(_ context.Context, table string, items []map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return err
	}

	for _, item := range items {
		m.putRow(t, item)
	}

	return nil
}

// BatchGetItems returns the rows present for the given keys, skipping the
// ones that are missing or expired.
func (m *Mock) BatchGetItems(_ context.Context, table string, keys []map[string]any) ([]map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.lookup(table)
	if err != nil {
		return nil, err
	}

	var out []map[string]any

	for _, key := range keys {
		item, ok := t.items.Get(itemKey(t, key))
		if !ok || m.expired(t, item) {
			continue
		}

		out = append(out, visible(item))
	}

	return out, nil
}

// TransactWriteItems applies puts and deletes together. Every CloudEmu
// mutation is synchronous and the whole batch runs under one lock, so the
// group is atomic with respect to other callers.
func (m *Mock) TransactWriteItems(
	_ context.Context, table string, puts []map[string]any, deletes []map[string]any,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.lookup(table)
	if err != nil {
		return err
	}

	for _, item := range puts {
		m.putRow(t, item)
	}

	for _, key := range deletes {
		t.items.Delete(itemKey(t, key))
	}

	return nil
}
