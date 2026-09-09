package grafana

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// UpdateWorkspaceAuthentication replaces the workspace's authentication
// providers and records whether a SAML configuration was supplied, which is
// reflected in the authentication summary's samlConfigurationStatus.
func (m *Mock) UpdateWorkspaceAuthentication(_ context.Context, in *driver.UpdateAuthenticationInput) (*driver.Workspace, error) {
	var updated driver.Workspace

	ok := m.workspaces.Update(in.ID, func(w driver.Workspace) driver.Workspace {
		if in.AuthenticationProviders != nil {
			w.AuthenticationProviders = copyStrings(in.AuthenticationProviders)
		}

		if in.SamlConfigured {
			w.SamlConfigurationStatus = driver.SamlConfigured
		}

		w.Modified = m.now()
		updated = w

		return w
	})
	if !ok {
		return nil, notFound(in.ID)
	}

	out := copyWorkspace(&updated)

	return &out, nil
}

// DescribeWorkspaceAuthentication returns a copy of the workspace whose
// authentication providers and samlConfigurationStatus the caller reads.
func (m *Mock) DescribeWorkspaceAuthentication(_ context.Context, id string) (*driver.Workspace, error) {
	return m.DescribeWorkspace(context.Background(), id)
}
