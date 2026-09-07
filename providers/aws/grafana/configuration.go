package grafana

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// UpdateWorkspaceConfiguration replaces the workspace's configuration string and
// optionally upgrades its grafanaVersion. A nil GrafanaVersion keeps the current
// version. The computed fields are preserved; modified is bumped. The workspace
// stays ACTIVE so an IaC waiter that blocks on status does not hang.
func (m *Mock) UpdateWorkspaceConfiguration(_ context.Context, in *driver.UpdateConfigurationInput) error {
	ok := m.workspaces.Update(in.ID, func(w driver.Workspace) driver.Workspace {
		w.Configuration = in.Configuration
		if in.GrafanaVersion != nil && *in.GrafanaVersion != "" {
			w.GrafanaVersion = *in.GrafanaVersion
		}

		w.Modified = m.now()

		return w
	})
	if !ok {
		return notFound(in.ID)
	}

	return nil
}

// DescribeWorkspaceConfiguration returns the workspace's configuration string
// and grafanaVersion, both stable across reads.
func (m *Mock) DescribeWorkspaceConfiguration(_ context.Context, id string) (configuration, grafanaVersion string, err error) {
	w, ok := m.workspaces.Get(id)
	if !ok {
		return "", "", notFound(id)
	}

	return w.Configuration, w.GrafanaVersion, nil
}
