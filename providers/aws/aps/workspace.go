package aps

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// CreateWorkspace provisions a new workspace directly in the ACTIVE state with
// stable computed fields (workspaceId, arn, prometheusEndpoint, createdAt). The
// alias, kmsKeyArn and tags are stored as supplied.
func (m *Mock) CreateWorkspace(_ context.Context, in *driver.CreateWorkspaceInput) (*driver.Workspace, error) {
	id := newWorkspaceID()
	now := m.now()

	ws := driver.Workspace{
		WorkspaceID:        id,
		Arn:                m.workspaceARN(id),
		Alias:              in.Alias,
		Status:             driver.StatusActive,
		KmsKeyArn:          in.KmsKeyArn,
		PrometheusEndpoint: m.prometheusEndpoint(id),
		CreatedAt:          now,
		Tags:               copyTags(in.Tags),
	}

	m.workspaces.Set(id, ws)

	out := copyWorkspace(&ws)

	return &out, nil
}

// DescribeWorkspace returns a copy of the workspace. The stored workspaceId,
// arn, status, prometheusEndpoint and createdAt are returned unchanged so
// repeated reads never drift.
func (m *Mock) DescribeWorkspace(_ context.Context, workspaceID string) (*driver.Workspace, error) {
	w, ok := m.workspaces.Get(workspaceID)
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	out := copyWorkspace(&w)

	return &out, nil
}

// UpdateWorkspaceAlias sets the workspace alias, leaving the computed fields
// untouched.
func (m *Mock) UpdateWorkspaceAlias(_ context.Context, workspaceID, alias string) error {
	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		w.Alias = alias

		return w
	})
	if !ok {
		return notFound("workspace %s not found", workspaceID)
	}

	return nil
}

// DeleteWorkspace removes a workspace and all of its child resources.
func (m *Mock) DeleteWorkspace(_ context.Context, workspaceID string) error {
	if !m.workspaces.Delete(workspaceID) {
		return notFound("workspace %s not found", workspaceID)
	}

	return nil
}

// ListWorkspaces returns a deterministic page of workspaces, optionally filtered
// by an alias prefix (matching the real APS alias filter).
func (m *Mock) ListWorkspaces(
	_ context.Context, alias string, page driver.Page,
) (workspaces []driver.Workspace, nextToken string, err error) {
	stored := m.workspaces.SortedValues()

	filtered := make([]driver.Workspace, 0, len(stored))

	for i := range stored {
		if alias != "" && !strings.HasPrefix(stored[i].Alias, alias) {
			continue
		}

		filtered = append(filtered, copyWorkspace(&stored[i]))
	}

	start, end, next := paginate(len(filtered), page)

	return filtered[start:end], next, nil
}
