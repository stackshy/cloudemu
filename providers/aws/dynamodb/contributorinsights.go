package dynamodb

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

var _ driver.ContributorInsighter = (*Mock)(nil)

// UpdateContributorInsights enables or disables Contributor Insights for the
// table (index empty) or one of its GSIs, landing the new status immediately.
func (m *Mock) UpdateContributorInsights(_ context.Context, table, index string, enable bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return "", cerrors.Newf(cerrors.NotFound, "Table not found: %s", table)
	}

	if index != "" {
		if !td.config.HasGSI(index) {
			return "", cerrors.Newf(cerrors.NotFound, "Index not found: %s", index)
		}
	}

	status := driver.ContributorInsightsDisabled
	if enable {
		status = driver.ContributorInsightsEnabled
	}

	if td.ci == nil {
		td.ci = make(map[string]ciRecord)
	}

	td.ci[index] = ciRecord{status: status, lastUpdateUnix: float64(m.opts.Clock.Now().Unix())}

	return status, nil
}

// DescribeContributorInsights returns the Contributor Insights status of the
// table or index (DISABLED when never enabled).
func (m *Mock) DescribeContributorInsights(_ context.Context,
	table, index string) (status string, lastUpdateUnix float64, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return "", 0, cerrors.Newf(cerrors.NotFound, "Table not found: %s", table)
	}

	if index != "" {
		if !td.config.HasGSI(index) {
			return "", 0, cerrors.Newf(cerrors.NotFound, "Index not found: %s", index)
		}
	}

	rec, ok := td.ci[index]
	if !ok {
		return driver.ContributorInsightsDisabled, 0, nil
	}

	return rec.status, rec.lastUpdateUnix, nil
}

// ListContributorInsights returns a summary for every table/index that has a
// Contributor Insights record, ordered by table then index. When table is
// non-empty only that table's records are returned.
func (m *Mock) ListContributorInsights(_ context.Context, table string) ([]driver.ContributorInsightsSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []driver.ContributorInsightsSummary

	for name, td := range m.tables {
		if table != "" && name != table {
			continue
		}

		for index, rec := range td.ci {
			out = append(out, driver.ContributorInsightsSummary{Table: name, Index: index, Status: rec.status})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}

		return out[i].Index < out[j].Index
	})

	return out, nil
}
