package grafana

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// CreateWorkspace provisions a new workspace directly in the ACTIVE state with
// stable computed fields (id, arn, endpoint, grafanaVersion, created). The
// nested vpcConfiguration and networkAccessControl blocks are carried verbatim
// so a DescribeWorkspace reflects exactly what the caller sent.
func (m *Mock) CreateWorkspace(_ context.Context, in *driver.CreateWorkspaceInput) (*driver.Workspace, error) {
	if in.AccountAccessType == "" {
		return nil, validation("accountAccessType is required")
	}

	if len(in.AuthenticationProviders) == 0 {
		return nil, validation("authenticationProviders is required")
	}

	if in.PermissionType == "" {
		return nil, validation("permissionType is required")
	}

	id := newID()
	now := m.now()

	ws := driver.Workspace{
		ID:                       id,
		Arn:                      m.workspaceARN(id),
		Name:                     in.Name,
		Description:              in.Description,
		Status:                   driver.StatusActive,
		Endpoint:                 m.workspaceEndpoint(id),
		GrafanaVersion:           firstNonEmpty(in.GrafanaVersion, defaultGrafanaVersion),
		Created:                  now,
		Modified:                 now,
		AccountAccessType:        in.AccountAccessType,
		PermissionType:           in.PermissionType,
		AuthenticationProviders:  copyStrings(in.AuthenticationProviders),
		SamlConfigurationStatus:  driver.SamlNotConfigured,
		DataSources:              copyStrings(in.DataSources),
		NotificationDestinations: copyStrings(in.NotificationDestinations),
		OrganizationalUnits:      copyStrings(in.OrganizationalUnits),
		OrganizationRoleName:     in.OrganizationRoleName,
		WorkspaceRoleArn:         in.RoleArn,
		StackSetName:             in.StackSetName,
		Configuration:            firstNonEmpty(in.Configuration, defaultConfiguration),
		VpcConfiguration:         copyRaw(in.VpcConfiguration),
		NetworkAccessControl:     copyRaw(in.NetworkAccessControl),
		Tags:                     copyTags(in.Tags),
	}

	m.workspaces.Set(id, ws)

	out := copyWorkspace(&ws)

	return &out, nil
}

// DescribeWorkspace returns a copy of the workspace. The stored computed fields
// are returned unchanged so repeated reads never drift.
func (m *Mock) DescribeWorkspace(_ context.Context, id string) (*driver.Workspace, error) {
	w, ok := m.workspaces.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	out := copyWorkspace(&w)

	return &out, nil
}

// UpdateWorkspace merges the fields the request supplied, leaving omitted
// optional parameters unchanged. The computed id, arn, endpoint,
// grafanaVersion, status and created are preserved; modified is bumped.
func (m *Mock) UpdateWorkspace(_ context.Context, in *driver.UpdateWorkspaceInput) (*driver.Workspace, error) {
	var updated driver.Workspace

	ok := m.workspaces.Update(in.ID, func(w driver.Workspace) driver.Workspace {
		applyWorkspaceUpdate(&w, in)
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

// applyWorkspaceUpdate applies the present fields of an UpdateWorkspaceInput onto
// a stored workspace. A nil pointer field means the parameter was omitted and
// the stored value is left unchanged.
func applyWorkspaceUpdate(w *driver.Workspace, in *driver.UpdateWorkspaceInput) {
	setString(&w.AccountAccessType, in.AccountAccessType)
	setString(&w.PermissionType, in.PermissionType)
	setString(&w.OrganizationRoleName, in.OrganizationRoleName)
	setString(&w.StackSetName, in.StackSetName)
	setString(&w.WorkspaceRoleArn, in.RoleArn)
	setString(&w.Name, in.Name)
	setString(&w.Description, in.Description)
	setStrings(&w.DataSources, in.DataSources)
	setStrings(&w.NotificationDestinations, in.NotificationDestinations)
	setStrings(&w.OrganizationalUnits, in.OrganizationalUnits)

	switch {
	case in.RemoveVpcConfiguration:
		w.VpcConfiguration = nil
	case in.VpcConfiguration != nil:
		w.VpcConfiguration = copyRaw(in.VpcConfiguration)
	}

	switch {
	case in.RemoveNetworkAccessConfiguration:
		w.NetworkAccessControl = nil
	case in.NetworkAccessControl != nil:
		w.NetworkAccessControl = copyRaw(in.NetworkAccessControl)
	}
}

// DeleteWorkspace removes a workspace and returns its final description with a
// DELETING status, matching the async DeleteWorkspace response. A subsequent
// DescribeWorkspace yields ResourceNotFoundException so an IaC delete waiter
// completes.
func (m *Mock) DeleteWorkspace(_ context.Context, id string) (*driver.Workspace, error) {
	w, ok := m.workspaces.Get(id)
	if !ok {
		return nil, notFound(id)
	}

	m.workspaces.Delete(id)

	out := copyWorkspace(&w)
	out.Status = driver.StatusDeleting

	return &out, nil
}

// ListWorkspaces returns a deterministic page of workspace summaries ordered by
// id.
func (m *Mock) ListWorkspaces(_ context.Context, page driver.Page) (workspaces []driver.Workspace, nextToken string, err error) {
	stored := m.workspaces.SortedValues()

	start, end, next := paginate(len(stored), page)

	out := make([]driver.Workspace, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyWorkspace(&stored[i]))
	}

	return out, next, nil
}

// setString overwrites the target with the pointed-to value when the update
// supplied it (a non-nil pointer), and leaves it unchanged otherwise.
func setString(dst, src *string) {
	if src != nil {
		*dst = *src
	}
}

// setStrings overwrites the target slice when the update supplied it.
func setStrings(dst, src *[]string) {
	if src != nil {
		*dst = copyStrings(*src)
	}
}

// firstNonEmpty returns a if it is non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
