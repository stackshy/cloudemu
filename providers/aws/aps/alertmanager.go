package aps

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// CreateAlertManagerDefinition sets the workspace's alert-manager definition,
// directly in the ACTIVE state. The definition blob is stored verbatim. A
// definition already present yields a ConflictException.
//
//nolint:dupl // parallel-shaped to CreateLoggingConfiguration but a distinct sub-resource type.
func (m *Mock) CreateAlertManagerDefinition(
	_ context.Context, workspaceID, data string,
) (*driver.AlertManagerDefinition, error) {
	var (
		result driver.AlertManagerDefinition
		dupErr error
	)

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		if w.AlertManager != nil {
			dupErr = conflict("alert manager definition already exists in workspace %s", workspaceID)

			return w
		}

		now := m.now()
		w.AlertManager = &driver.AlertManagerDefinition{
			Data:       data,
			Status:     driver.StatusActive,
			CreatedAt:  now,
			ModifiedAt: now,
		}
		result = *w.AlertManager

		return w
	})
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	if dupErr != nil {
		return nil, dupErr
	}

	out := result

	return &out, nil
}

// PutAlertManagerDefinition creates or replaces the workspace's alert-manager
// definition. On replace the createdAt is preserved; the blob and modifiedAt are
// updated.
func (m *Mock) PutAlertManagerDefinition(
	_ context.Context, workspaceID, data string,
) (*driver.AlertManagerDefinition, error) {
	var result driver.AlertManagerDefinition

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		now := m.now()

		created := now
		if w.AlertManager != nil {
			created = w.AlertManager.CreatedAt
		}

		w.AlertManager = &driver.AlertManagerDefinition{
			Data:       data,
			Status:     driver.StatusActive,
			CreatedAt:  created,
			ModifiedAt: now,
		}
		result = *w.AlertManager

		return w
	})
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	out := result

	return &out, nil
}

// DescribeAlertManagerDefinition returns a copy of the workspace's alert-manager
// definition.
func (m *Mock) DescribeAlertManagerDefinition(
	_ context.Context, workspaceID string,
) (*driver.AlertManagerDefinition, error) {
	w, ok := m.workspaces.Get(workspaceID)
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	if w.AlertManager == nil {
		return nil, notFound("alert manager definition not found in workspace %s", workspaceID)
	}

	out := *w.AlertManager

	return &out, nil
}

// DeleteAlertManagerDefinition removes the workspace's alert-manager definition.
func (m *Mock) DeleteAlertManagerDefinition(_ context.Context, workspaceID string) error {
	var missing bool

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		if w.AlertManager == nil {
			missing = true

			return w
		}

		w.AlertManager = nil

		return w
	})
	if !ok {
		return notFound("workspace %s not found", workspaceID)
	}

	if missing {
		return notFound("alert manager definition not found in workspace %s", workspaceID)
	}

	return nil
}
