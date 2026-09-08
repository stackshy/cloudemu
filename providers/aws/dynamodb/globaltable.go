package dynamodb

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

var _ driver.GlobalTabler = (*Mock)(nil)

// CreateGlobalTable creates a version-2017 global table named after an existing
// table with the given replica regions, landing ACTIVE immediately.
func (m *Mock) CreateGlobalTable(_ context.Context, name string, regions []string) (driver.GlobalTableInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tables[name]; !exists {
		return driver.GlobalTableInfo{}, cerrors.Newf(cerrors.NotFound, "Table not found: %s", name)
	}

	if _, exists := m.globalTables[name]; exists {
		return driver.GlobalTableInfo{}, cerrors.Newf(cerrors.AlreadyExists, "Global table already exists: %s", name)
	}

	info := &driver.GlobalTableInfo{
		Name:        name,
		Arn:         idgen.AWSARN("dynamodb", m.opts.Region, m.opts.AccountID, "global-table/"+name),
		Status:      driver.GlobalTableStatusActive,
		CreatedUnix: float64(m.opts.Clock.Now().Unix()),
		Regions:     dedupRegions(regions),
	}
	m.globalTables[name] = info

	return copyGlobalTable(info), nil
}

// DescribeGlobalTable returns the global table identified by name.
func (m *Mock) DescribeGlobalTable(_ context.Context, name string) (driver.GlobalTableInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, ok := m.globalTables[name]
	if !ok {
		return driver.GlobalTableInfo{}, cerrors.Newf(cerrors.NotFound, "Global table not found: %s", name)
	}

	return copyGlobalTable(info), nil
}

// UpdateGlobalTable adds and/or removes replica regions.
func (m *Mock) UpdateGlobalTable(_ context.Context,
	name string, addRegions, removeRegions []string) (driver.GlobalTableInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, ok := m.globalTables[name]
	if !ok {
		return driver.GlobalTableInfo{}, cerrors.Newf(cerrors.NotFound, "Global table not found: %s", name)
	}

	present := make(map[string]bool, len(info.Regions))
	for _, r := range info.Regions {
		present[r] = true
	}

	for _, r := range addRegions {
		if present[r] {
			// AlreadyExists here is a duplicate replica (rendered as
			// ReplicaAlreadyExistsException); the global-table-exists case is
			// caught by CreateGlobalTable, so update never conflates them.
			return driver.GlobalTableInfo{}, cerrors.Newf(cerrors.AlreadyExists, "Replica already exists: %s", r)
		}

		present[r] = true
	}

	for _, r := range removeRegions {
		if !present[r] {
			// FailedPrecondition distinguishes a missing replica (rendered as
			// ReplicaNotFoundException) from a missing global table (NotFound,
			// rendered as GlobalTableNotFoundException).
			return driver.GlobalTableInfo{}, cerrors.Newf(cerrors.FailedPrecondition, "Replica not found: %s", r)
		}

		delete(present, r)
	}

	info.Regions = sortedKeys(present)

	return copyGlobalTable(info), nil
}

// ListGlobalTables returns every global table ordered by name, optionally
// filtered to those with a replica in regionFilter.
func (m *Mock) ListGlobalTables(_ context.Context, regionFilter string) ([]driver.GlobalTableInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]driver.GlobalTableInfo, 0, len(m.globalTables))

	for _, info := range m.globalTables {
		if regionFilter != "" && !containsRegion(info.Regions, regionFilter) {
			continue
		}

		out = append(out, copyGlobalTable(info))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// copyGlobalTable deep-copies a stored global table so callers never alias the
// provider's region slice.
func copyGlobalTable(info *driver.GlobalTableInfo) driver.GlobalTableInfo {
	out := *info
	out.Regions = append([]string(nil), info.Regions...)

	return out
}

// dedupRegions returns regions with duplicates removed, in stable sorted order.
func dedupRegions(regions []string) []string {
	seen := make(map[string]bool, len(regions))
	for _, r := range regions {
		seen[r] = true
	}

	return sortedKeys(seen)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func containsRegion(regions []string, target string) bool {
	for _, r := range regions {
		if r == target {
			return true
		}
	}

	return false
}
