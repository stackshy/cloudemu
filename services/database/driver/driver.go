// Package driver defines the interface for database service implementations.
package driver

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// TTLConfig configures TTL for a table.
type TTLConfig struct {
	Enabled       bool
	AttributeName string // the field containing the TTL timestamp
}

// StreamRecord represents a change event in a stream.
type StreamRecord struct {
	EventID        string
	EventType      string // "INSERT", "MODIFY", "REMOVE"
	Table          string
	Keys           map[string]any
	NewImage       map[string]any
	OldImage       map[string]any
	Timestamp      time.Time
	SequenceNumber string
}

// StreamConfig configures streams for a table.
type StreamConfig struct {
	Enabled  bool
	ViewType string // "NEW_IMAGE", "OLD_IMAGE", "NEW_AND_OLD_IMAGES", "KEYS_ONLY"
}

// StreamIterator allows reading stream records.
type StreamIterator struct {
	ShardID   string
	Records   []StreamRecord
	NextToken string
}

// TableConfig describes a table to create.
type TableConfig struct {
	Name         string
	PartitionKey string
	SortKey      string
	GSIs         []GSIConfig
}

// GSIConfig describes a Global Secondary Index.
type GSIConfig struct {
	Name         string
	PartitionKey string
	SortKey      string
}

// UpdateAction represents a single field-level update action. It is the legacy,
// pre-expression update form; the wire handler uses UpdateExpression instead.
type UpdateAction struct {
	Action string // "SET" or "REMOVE"
	Field  string
	Value  any // ignored for REMOVE
}

// UpdateItemInput describes an update operation on an existing item. When
// UpdateExpression is set it takes precedence and is evaluated with full
// DynamoDB grammar (SET arithmetic, if_not_exists, list_append, ADD, DELETE);
// otherwise the legacy Actions are applied. ExprNames/ExprValues resolve the
// expression's #aliases and :placeholders.
type UpdateItemInput struct {
	Table            string
	Key              map[string]any
	Actions          []UpdateAction
	UpdateExpression string
	ExprNames        map[string]string
	ExprValues       map[string]any
}

// ApplyUpdate applies input's update to a caller-owned copy of item, mutating
// it in place and returning it. It runs the full UpdateExpression grammar when
// input.UpdateExpression is set, otherwise the legacy SET/REMOVE Actions. It is
// shared by every database provider so update semantics stay identical across
// clouds.
//
//nolint:gocritic // hugeParam: input mirrors the driver's UpdateItem signature.
func ApplyUpdate(item map[string]any, input UpdateItemInput) (map[string]any, error) {
	if input.UpdateExpression != "" {
		prog, err := expr.ParseUpdate(input.UpdateExpression, input.ExprNames, input.ExprValues)
		if err != nil {
			return nil, err
		}

		return expr.ApplyUpdate(item, prog)
	}

	for _, action := range input.Actions {
		switch action.Action {
		case "SET":
			item[action.Field] = action.Value
		case "REMOVE":
			delete(item, action.Field)
		default:
			return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported action: %s", action.Action)
		}
	}

	return item, nil
}

// KeyCondition defines a key condition for queries.
type KeyCondition struct {
	PartitionKey string
	PartitionVal any
	SortOp       string // "=", "<", ">", "<=", ">=", "BETWEEN", "BEGINS_WITH"
	SortVal      any
	SortValEnd   any // for BETWEEN
}

// ScanFilter defines a scan filter.
type ScanFilter struct {
	Field string
	Op    string // "=", "!=", "<", ">", "<=", ">=", "CONTAINS", "BEGINS_WITH"
	Value any
}

// QueryInput configures a query operation.
type QueryInput struct {
	Table        string
	IndexName    string
	KeyCondition KeyCondition
	// Filters is the legacy pre-simplified post-key-condition filter, applied
	// to items that already match the key condition (same semantics as
	// Scan.Filters). Retained for back-compat; ignored when FilterExpression
	// is set.
	Filters []ScanFilter

	// FilterExpression is the raw DynamoDB FilterExpression. When non-empty it
	// is parsed and evaluated with full grammar fidelity (functions, boolean
	// operators, IN/BETWEEN, type-aware comparisons), taking precedence over
	// the legacy Filters. ExprNames/ExprValues resolve #alias and :placeholder
	// tokens (values already decoded to native Go).
	FilterExpression string
	ExprNames        map[string]string
	ExprValues       map[string]any

	Limit     int
	PageToken string

	// ExclusiveStartKey selects key-based continuation (DynamoDB-style):
	// the page starts after the item with these key attributes. Mutually
	// exclusive with PageToken.
	ExclusiveStartKey map[string]any

	// SortDescending reverses the stable key ordering before paging.
	// The zero value (ascending) matches the historical behavior.
	SortDescending bool

	// Deprecated: never implemented; use SortDescending. Retained so
	// existing constructors keep compiling.
	ScanForward bool
}

// ScanInput configures a scan operation.
type ScanInput struct {
	Table string
	// Filters is the legacy pre-simplified filter; ignored when
	// FilterExpression is set. Retained for back-compat.
	Filters []ScanFilter

	// FilterExpression is the raw DynamoDB FilterExpression, evaluated with
	// full grammar fidelity when non-empty (see QueryInput.FilterExpression).
	FilterExpression string
	ExprNames        map[string]string
	ExprValues       map[string]any

	Limit     int
	PageToken string

	// ExclusiveStartKey selects key-based continuation; see QueryInput.
	ExclusiveStartKey map[string]any
}

// QueryResult is the result of a query or scan.
type QueryResult struct {
	Items         []map[string]any
	Count         int
	NextPageToken string

	// LastEvaluatedKey holds the key attributes of the last returned item
	// whenever more items remain, enabling DynamoDB-style continuation.
	LastEvaluatedKey map[string]any
}

// IndexInfo describes a Global Secondary Index.
type IndexInfo struct {
	Name         string
	PartitionKey string
	SortKey      string
	Status       string // "ACTIVE", "CREATING", "DELETING"
}

// Database is the interface that database provider implementations must satisfy.
// AccountAttributes are the Cosmos-DB-account cost/identity attributes a real
// Azure `documentdb/databaseaccounts` resource carries but a DynamoDB/Firestore
// table does not. Surfaced through the optional TableAttributes capability.
type AccountAttributes struct {
	Kind           string   // GlobalDocumentDB / MongoDB
	OfferType      string   // databaseAccountOfferType (Standard)
	EnableFreeTier bool     // free-tier flag (cost)
	Capabilities   []string // e.g. EnableServerless (cost)
}

// TableAttributes is an OPTIONAL capability, discovered by type assertion (like
// the storage BucketAttributes capability): a provider whose tables map to a
// richer account resource (Azure Cosmos DB) exposes the account's cost
// attributes. DynamoDB/Firestore don't implement it and contribute nothing.
type TableAttributes interface {
	TableAttributes(ctx context.Context, table string) (AccountAttributes, error)
}

type Database interface {
	CreateTable(ctx context.Context, config TableConfig) error
	DeleteTable(ctx context.Context, name string) error
	DescribeTable(ctx context.Context, name string) (*TableConfig, error)
	ListTables(ctx context.Context) ([]string, error)

	PutItem(ctx context.Context, table string, item map[string]any) error
	GetItem(ctx context.Context, table string, key map[string]any) (map[string]any, error)
	UpdateItem(ctx context.Context, input UpdateItemInput) (map[string]any, error)
	DeleteItem(ctx context.Context, table string, key map[string]any) error
	Query(ctx context.Context, input QueryInput) (*QueryResult, error)
	Scan(ctx context.Context, input ScanInput) (*QueryResult, error)

	BatchPutItems(ctx context.Context, table string, items []map[string]any) error
	BatchGetItems(ctx context.Context, table string, keys []map[string]any) ([]map[string]any, error)

	// TTL
	UpdateTTL(ctx context.Context, table string, config TTLConfig) error
	DescribeTTL(ctx context.Context, table string) (*TTLConfig, error)

	// Streams / Change Feed
	UpdateStreamConfig(ctx context.Context, table string, config StreamConfig) error
	GetStreamRecords(ctx context.Context, table string, limit int, token string) (*StreamIterator, error)

	// Transactional writes
	TransactWriteItems(ctx context.Context, table string, puts []map[string]any, deletes []map[string]any) error

	// Global Secondary Indexes
	CreateIndex(ctx context.Context, table string, config GSIConfig) (*IndexInfo, error)
	DeleteIndex(ctx context.Context, table, indexName string) error
	DescribeIndex(ctx context.Context, table, indexName string) (*IndexInfo, error)
	ListIndexes(ctx context.Context, table string) ([]IndexInfo, error)

	// Tagging
	TagResource(ctx context.Context, table string, tags map[string]string) error
	UntagResource(ctx context.Context, table string, tagKeys []string) error
	ListTagsOfResource(ctx context.Context, table string) (map[string]string, error)
}

// CompareValues orders two attribute values the way the real services do:
// numbers numerically, strings lexically, bools false<true. Mixed types
// order by a fixed type rank so the ordering stays total and deterministic
// (numbers < strings < bools < everything else).
func CompareValues(a, b any) int {
	an, aIsNum := toFloat(a)
	bn, bIsNum := toFloat(b)

	switch {
	case aIsNum && bIsNum:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aIsNum:
		return -1
	case bIsNum:
		return 1
	}

	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)

	switch {
	case aIsStr && bIsStr:
		return strings.Compare(as, bs)
	case aIsStr:
		return -1
	case bIsStr:
		return 1
	}

	ab, aIsBool := a.(bool)
	bb, bIsBool := b.(bool)

	switch {
	case aIsBool && bIsBool:
		switch {
		case !ab && bb:
			return -1
		case ab && !bb:
			return 1
		}
		return 0
	case aIsBool:
		return -1
	case bIsBool:
		return 1
	}

	return strings.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// SortByFields stably orders items by the given attribute fields in order
// (typically partition key, then sort key), comparing values type-aware via
// CompareValues. A stable, deterministic ordering is what keeps offset-based
// page tokens from internal/pagination valid across calls.
func SortByFields(items []map[string]any, fields ...string) {
	sort.SliceStable(items, func(i, j int) bool {
		for _, f := range fields {
			if f == "" {
				continue
			}
			if c := CompareValues(items[i][f], items[j][f]); c != 0 {
				return c < 0
			}
		}
		return false
	})
}

// PageByKey slices one page out of a stably-ordered result set using
// key-based continuation: the page starts after the item whose identity
// matches startKey. A startKey that matches no item is an error — silently
// restarting from the beginning would re-serve consumed items.
func PageByKey(
	items []map[string]any,
	startKey map[string]any,
	limit int,
	identity func(map[string]any) string,
) (page []map[string]any, more bool, err error) {
	if limit <= 0 {
		limit = 100
	}

	start := 0
	if len(startKey) > 0 {
		want := identity(startKey)
		found := false
		for i, it := range items {
			if identity(it) == want {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, false, fmt.Errorf("start key matches no item")
		}
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	return items[start:end], end < len(items), nil
}

// KeyAttributes extracts the named fields from an item — the shape handed
// back as LastEvaluatedKey. Empty field names are skipped.
func KeyAttributes(item map[string]any, fields ...string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if f == "" {
			continue
		}
		if v, ok := item[f]; ok {
			out[f] = v
		}
	}
	return out
}

// PageOrdered is the one paging path for query/scan results: it stably
// orders matched items by the table keys, optionally reverses for
// descending queries, then slices one page — key-based continuation when
// startKey is set, offset tokens otherwise. LastEvaluatedKey is populated
// whenever more items remain, on both paths.
// orderPK/orderSK are the fields to order by (the index keys for GSI
// queries); keyFields is the LastEvaluatedKey shape (base table keys, plus
// the index keys for GSI queries, matching real DynamoDB).
func PageOrdered(
	matched []map[string]any,
	orderPK, orderSK string,
	keyFields []string,
	limit int,
	pageToken string,
	startKey map[string]any,
	descending bool,
	identity func(map[string]any) string,
) (*QueryResult, error) {
	SortByFields(matched, orderPK, orderSK)

	if descending {
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
	}

	if len(startKey) > 0 {
		items, more, err := PageByKey(matched, startKey, limit, identity)
		if err != nil {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid ExclusiveStartKey")
		}

		res := &QueryResult{Items: cloneItems(items), Count: len(items)}
		if more && len(items) > 0 {
			res.LastEvaluatedKey = KeyAttributes(items[len(items)-1], keyFields...)
		}
		return res, nil
	}

	page, err := pagination.Paginate(matched, pageToken, limit)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	res := &QueryResult{Items: cloneItems(page.Items), Count: len(page.Items), NextPageToken: page.NextPageToken}
	if page.HasMore && len(page.Items) > 0 {
		res.LastEvaluatedKey = KeyAttributes(page.Items[len(page.Items)-1], keyFields...)
	}
	return res, nil
}

// cloneItems shallow-copies each result item so callers mutating top-level
// keys of what they receive cannot corrupt the store. Nested map/slice
// values are still shared — deep-copying arbitrary attribute trees is a
// deliberate non-goal (documented limitation).
func cloneItems(items []map[string]any) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = maps.Clone(it)
	}
	return out
}
