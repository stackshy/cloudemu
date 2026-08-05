package lambda

import (
	"context"
	"encoding/json"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// AddPermission adds a statement to a function's resource-based policy. This
// backs Terraform's aws_lambda_permission and the grants S3/SNS/EventBridge
// create to invoke a function. The emulator stores statements without
// evaluating them — invocation is never actually denied.
func (m *Mock) AddPermission(_ context.Context, functionName string, stmt driver.PermissionStatement) error {
	if stmt.StatementID == "" {
		return cerrors.New(cerrors.InvalidArgument, "StatementId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if fd.policy == nil {
		fd.policy = make(map[string]driver.PermissionStatement)
	}

	if _, exists := fd.policy[stmt.StatementID]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "statement %s already exists", stmt.StatementID)
	}

	fd.policy[stmt.StatementID] = stmt
	m.funcs.Set(functionName, fd)

	return nil
}

// RemovePermission drops a statement from a function's resource-based policy.
func (m *Mock) RemovePermission(_ context.Context, functionName, statementID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if _, exists := fd.policy[statementID]; !exists {
		return cerrors.Newf(cerrors.NotFound, "statement %s not found", statementID)
	}

	delete(fd.policy, statementID)
	m.funcs.Set(functionName, fd)

	return nil
}

// GetPolicy returns the function's resource-based policy as a JSON document,
// matching the shape the AWS SDK expects (IAM policy with Sid/Principal/
// Action/Resource per statement). Returns NotFound when no policy exists.
func (m *Mock) GetPolicy(_ context.Context, functionName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if len(fd.policy) == 0 {
		return "", cerrors.Newf(cerrors.NotFound, "no policy for function %s", functionName)
	}

	statements := make([]map[string]any, 0, len(fd.policy))
	for _, s := range fd.policy {
		stmt := map[string]any{
			"Sid":       s.StatementID,
			"Effect":    "Allow",
			"Principal": map[string]string{"Service": s.Principal},
			"Action":    s.Action,
			"Resource":  fd.info.ARN,
		}
		if s.SourceARN != "" {
			stmt["Condition"] = map[string]any{
				"ArnLike": map[string]string{"AWS:SourceArn": s.SourceARN},
			}
		}

		statements = append(statements, stmt)
	}

	doc, err := json.Marshal(map[string]any{
		"Version":   "2012-10-17",
		"Id":        "default",
		"Statement": statements,
	})
	if err != nil {
		return "", err
	}

	return string(doc), nil
}
