package cosmosdb

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// checkUniqueKeys enforces the container's UniqueKeyPolicy against candidate,
// a document about to be created. Real Cosmos scopes a unique key constraint
// to a single partition-key value, so only documents sharing candidate's
// partition are considered; a document missing one of the constraint's paths
// never collides (real Cosmos ignores an unset property when checking
// uniqueness, rather than treating it as a matching null).
func (h *Handler) checkUniqueKeys(ctx context.Context, table string, cfg *dbdriver.TableConfig, candidate map[string]any) error {
	attrs := h.attrs.get(table)
	if len(attrs.uniqueKeys) == 0 {
		return nil
	}

	result, err := h.db.Scan(ctx, dbdriver.ScanInput{Table: table, Limit: allResults})
	if err != nil {
		return err
	}

	selfID, _ := candidate[idAttr].(string)
	candidatePK := partitionValue(cfg, candidate)

	for _, uk := range attrs.uniqueKeys {
		vals, ok := uniqueKeyTuple(candidate, uk.Paths)
		if !ok {
			continue
		}

		if conflictsWithinPartition(result.Items, cfg, selfID, candidatePK, vals, uk.Paths) {
			return cerrors.New(cerrors.AlreadyExists, "unique index constraint violation")
		}
	}

	return nil
}

func conflictsWithinPartition(
	items []map[string]any, cfg *dbdriver.TableConfig, selfID, candidatePK string, vals []any, paths []string,
) bool {
	for _, other := range items {
		if otherID, _ := other[idAttr].(string); otherID == selfID {
			continue
		}

		if cfg.PartitionKey != "" && partitionValue(cfg, other) != candidatePK {
			continue
		}

		otherVals, ok := uniqueKeyTuple(other, paths)
		if ok && equalTuples(vals, otherVals) {
			return true
		}
	}

	return false
}

func partitionValue(cfg *dbdriver.TableConfig, item map[string]any) string {
	if cfg.PartitionKey == "" {
		return ""
	}

	return fmt.Sprintf("%v", item[cfg.PartitionKey])
}

// uniqueKeyTuple resolves every path in a unique-key constraint against item,
// returning ok=false when any path is unset (such a document never
// participates in that constraint, matching real Cosmos).
func uniqueKeyTuple(item map[string]any, paths []string) ([]any, bool) {
	vals := make([]any, 0, len(paths))

	for _, p := range paths {
		v, ok := uniqueKeyValue(item, p)
		if !ok {
			return nil, false
		}

		vals = append(vals, v)
	}

	return vals, true
}

// uniqueKeyValue walks a Cosmos property path ("/a/b") over nested maps.
func uniqueKeyValue(item map[string]any, path string) (any, bool) {
	var cur any = item

	for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		v, ok := m[seg]
		if !ok {
			return nil, false
		}

		cur = v
	}

	return cur, true
}

func equalTuples(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if fmt.Sprintf("%v", a[i]) != fmt.Sprintf("%v", b[i]) {
			return false
		}
	}

	return true
}
