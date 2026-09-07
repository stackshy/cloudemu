package aps

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// CreateLoggingConfiguration sets the workspace's logging configuration,
// directly in the ACTIVE state. A configuration already present yields a
// ConflictException.
//
//nolint:dupl // parallel-shaped to CreateAlertManagerDefinition but a distinct sub-resource type.
func (m *Mock) CreateLoggingConfiguration(
	_ context.Context, workspaceID, logGroupArn string,
) (*driver.LoggingConfiguration, error) {
	var (
		result driver.LoggingConfiguration
		dupErr error
	)

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		if w.Logging != nil {
			dupErr = conflict("logging configuration already exists in workspace %s", workspaceID)

			return w
		}

		now := m.now()
		w.Logging = &driver.LoggingConfiguration{
			LogGroupArn: logGroupArn,
			Status:      driver.StatusActive,
			CreatedAt:   now,
			ModifiedAt:  now,
		}
		result = *w.Logging

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

// UpdateLoggingConfiguration replaces the log-group ARN of an existing logging
// configuration, preserving createdAt and bumping modifiedAt.
func (m *Mock) UpdateLoggingConfiguration(
	_ context.Context, workspaceID, logGroupArn string,
) (*driver.LoggingConfiguration, error) {
	var (
		result  driver.LoggingConfiguration
		missing bool
	)

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		if w.Logging == nil {
			missing = true

			return w
		}

		now := m.now()
		w.Logging = &driver.LoggingConfiguration{
			LogGroupArn: logGroupArn,
			Status:      driver.StatusActive,
			CreatedAt:   w.Logging.CreatedAt,
			ModifiedAt:  now,
		}
		result = *w.Logging

		return w
	})
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	if missing {
		return nil, notFound("logging configuration not found in workspace %s", workspaceID)
	}

	out := result

	return &out, nil
}

// DescribeLoggingConfiguration returns a copy of the workspace's logging
// configuration.
func (m *Mock) DescribeLoggingConfiguration(
	_ context.Context, workspaceID string,
) (*driver.LoggingConfiguration, error) {
	w, ok := m.workspaces.Get(workspaceID)
	if !ok {
		return nil, notFound("workspace %s not found", workspaceID)
	}

	if w.Logging == nil {
		return nil, notFound("logging configuration not found in workspace %s", workspaceID)
	}

	out := *w.Logging

	return &out, nil
}

// DeleteLoggingConfiguration removes the workspace's logging configuration.
func (m *Mock) DeleteLoggingConfiguration(_ context.Context, workspaceID string) error {
	var missing bool

	ok := m.workspaces.Update(workspaceID, func(w driver.Workspace) driver.Workspace {
		if w.Logging == nil {
			missing = true

			return w
		}

		w.Logging = nil

		return w
	})
	if !ok {
		return notFound("workspace %s not found", workspaceID)
	}

	if missing {
		return notFound("logging configuration not found in workspace %s", workspaceID)
	}

	return nil
}
