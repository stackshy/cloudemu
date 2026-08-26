package cloudwatch

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// storedDashboard is a persisted CloudWatch dashboard.
type storedDashboard struct {
	Name         string
	Body         string
	ARN          string
	LastModified time.Time
	Size         int
}

// PutDashboard creates or overwrites a dashboard. The DashboardBody must be a
// valid JSON document, matching the real CloudWatch DashboardInvalidInputError
// validation; an existing dashboard with the same name is replaced.
func (m *Mock) PutDashboard(_ context.Context, name, body string) error {
	if name == "" {
		return errors.Newf(errors.InvalidArgument, "dashboard name is required")
	}

	if !json.Valid([]byte(body)) {
		return errors.Newf(errors.InvalidArgument, "the dashboard body for %q is not valid JSON", name)
	}

	d := &storedDashboard{
		Name:         name,
		Body:         body,
		ARN:          idgen.AWSARN("cloudwatch", m.opts.Region, m.opts.AccountID, "dashboard/"+name),
		LastModified: m.opts.Clock.Now(),
		Size:         len(body),
	}

	m.dashboards.Set(name, d)

	return nil
}

// GetDashboard returns the named dashboard, or NotFound (the CloudWatch
// DashboardNotFoundError) when it does not exist.
func (m *Mock) GetDashboard(_ context.Context, name string) (*driver.DashboardInfo, error) {
	d, ok := m.dashboards.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "dashboard %q not found", name)
	}

	return &driver.DashboardInfo{
		Name:         d.Name,
		Body:         d.Body,
		ARN:          d.ARN,
		LastModified: d.LastModified,
		Size:         d.Size,
	}, nil
}

// ListDashboards returns dashboard summaries (no body) whose name starts with
// prefix, sorted by name. An empty prefix lists all dashboards.
func (m *Mock) ListDashboards(_ context.Context, prefix string) ([]driver.DashboardEntry, error) {
	all := m.dashboards.All()
	out := make([]driver.DashboardEntry, 0, len(all))

	for _, d := range all {
		if prefix != "" && !strings.HasPrefix(d.Name, prefix) {
			continue
		}

		out = append(out, driver.DashboardEntry{
			Name:         d.Name,
			ARN:          d.ARN,
			LastModified: d.LastModified,
			Size:         d.Size,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// DeleteDashboards deletes every named dashboard. It mirrors the real API's
// all-or-nothing semantics: if any name is missing, NotFound is returned and no
// dashboard is deleted.
func (m *Mock) DeleteDashboards(_ context.Context, names []string) error {
	for _, name := range names {
		if _, ok := m.dashboards.Get(name); !ok {
			return errors.Newf(errors.NotFound, "dashboard %q not found", name)
		}
	}

	for _, name := range names {
		m.dashboards.Delete(name)
	}

	return nil
}
