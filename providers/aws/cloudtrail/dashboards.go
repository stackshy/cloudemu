package cloudtrail

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// CreateDashboard stores a dashboard (keyed by name) and returns it.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) CreateDashboard(_ context.Context, in driver.Dashboard) (*driver.Dashboard, error) {
	if in.Name == "" {
		return nil, errInvalidParameter("Name is required")
	}

	now := m.now()
	in.ARN = m.dashboardARN(in.Name)
	in.CreatedAt = now
	in.UpdatedAt = now

	if in.Type == "" {
		in.Type = "CUSTOM"
	}

	in.Status = "CREATED"
	in.Tags = copyTags(in.Tags)

	dd := &dashboardData{dashboard: in}

	if !m.dashboards.SetIfAbsent(in.Name, dd) {
		return nil, errInvalidParameter("dashboard %q already exists", in.Name)
	}

	m.storeResourceTags(in.ARN, in.Tags)

	out := dd.dashboard

	return &out, nil
}

// resolveDashboard finds a dashboard by name or ARN.
func (m *Mock) resolveDashboard(nameOrARN string) (*dashboardData, error) {
	name := nameOrARN

	if strings.HasPrefix(nameOrARN, "arn:") {
		if i := strings.LastIndex(nameOrARN, "dashboard/"); i >= 0 {
			name = nameOrARN[i+len("dashboard/"):]
		}
	}

	dd, ok := m.dashboards.Get(name)
	if !ok {
		return nil, errResourceNotFound(nameOrARN)
	}

	return dd, nil
}

// GetDashboard returns a dashboard by name or ARN.
func (m *Mock) GetDashboard(_ context.Context, nameOrARN string) (*driver.Dashboard, error) {
	dd, err := m.resolveDashboard(nameOrARN)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	out := dd.dashboard

	return &out, nil
}

// UpdateDashboard applies new refresh schedule / termination protection.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) UpdateDashboard(_ context.Context, in driver.Dashboard) (*driver.Dashboard, error) {
	dd, err := m.resolveDashboard(in.Name)
	if err != nil {
		return nil, err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	d := &dd.dashboard
	d.TerminationProtectionEnabled = in.TerminationProtectionEnabled

	if in.RefreshScheduleFrequencyUnit != "" {
		d.RefreshScheduleFrequencyUnit = in.RefreshScheduleFrequencyUnit
		d.RefreshScheduleFrequencyVal = in.RefreshScheduleFrequencyVal
		d.RefreshScheduleStatus = in.RefreshScheduleStatus
	}

	d.UpdatedAt = m.now()
	out := *d

	return &out, nil
}

// DeleteDashboard removes a dashboard and its tags.
func (m *Mock) DeleteDashboard(_ context.Context, nameOrARN string) error {
	dd, err := m.resolveDashboard(nameOrARN)
	if err != nil {
		return err
	}

	dd.mu.RLock()
	name := dd.dashboard.Name
	arn := dd.dashboard.ARN
	dd.mu.RUnlock()

	m.dashboards.Delete(name)
	m.deleteResourceTags(arn)

	return nil
}

// ListDashboards returns all dashboards ordered by name, paginated.
func (m *Mock) ListDashboards(
	_ context.Context, nextToken string, maxResults int32,
) ([]driver.Dashboard, string, error) {
	all := m.dashboards.All()

	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}

	sort.Strings(names)

	limit := int(maxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	out := make([]driver.Dashboard, 0, len(names))
	started := nextToken == ""

	for _, n := range names {
		if !started {
			if n == nextToken {
				started = true
			}

			continue
		}

		if len(out) == limit {
			return out, out[len(out)-1].Name, nil
		}

		dd := all[n]
		dd.mu.RLock()
		out = append(out, dd.dashboard)
		dd.mu.RUnlock()
	}

	return out, "", nil
}

// StartDashboardRefresh triggers a refresh and returns a synthesized refresh ID.
// There is no real data behind a dashboard, so the refresh completes instantly.
func (m *Mock) StartDashboardRefresh(_ context.Context, nameOrARN string) (string, error) {
	if _, err := m.resolveDashboard(nameOrARN); err != nil {
		return "", err
	}

	return idgen.GenerateID("refresh-"), nil
}
