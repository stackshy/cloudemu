package lambda

import (
	"context"
	"encoding/json"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// accountIDLen is the length of an AWS account id (12 digits). An account-id
// principal renders as the account's IAM root ARN.
const accountIDLen = 12

// policyKey normalizes a Qualifier into the resource-policy map key. AWS keeps a
// separate policy per version/alias; an empty or "$LATEST" qualifier is the
// function's unqualified policy.
func policyKey(qualifier string) string {
	if qualifier == "" {
		return latestVersion
	}

	return qualifier
}

// AddPermission adds a statement to a function's resource-based policy. This
// backs Terraform's aws_lambda_permission and the grants S3/SNS/EventBridge
// create to invoke a function. A Qualifier scopes the statement to a single
// published version or alias — AWS stores a separate policy per qualifier. The
// emulator stores statements without evaluating them — invocation is never
// actually denied.
func (m *Mock) AddPermission(_ context.Context, functionName, qualifier string, stmt driver.PermissionStatement) error {
	if stmt.StatementID == "" {
		return cerrors.New(cerrors.InvalidArgument, "StatementId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	// A qualifier must name a version/alias that exists; a grant on a missing one
	// is a ResourceNotFoundException, matching real Lambda.
	if _, err := m.resolveQualifier(&fd, qualifier); err != nil {
		return err
	}

	if fd.policies == nil {
		fd.policies = make(map[string]map[string]driver.PermissionStatement)
	}

	key := policyKey(qualifier)
	if fd.policies[key] == nil {
		fd.policies[key] = make(map[string]driver.PermissionStatement)
	}

	if _, exists := fd.policies[key][stmt.StatementID]; exists {
		return cerrors.Newf(cerrors.AlreadyExists, "statement %s already exists", stmt.StatementID)
	}

	fd.policies[key][stmt.StatementID] = stmt
	m.funcs.Set(functionName, fd)

	return nil
}

// RemovePermission drops a statement from a function's (qualifier-scoped)
// resource-based policy.
func (m *Mock) RemovePermission(_ context.Context, functionName, qualifier, statementID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	key := policyKey(qualifier)
	if _, exists := fd.policies[key][statementID]; !exists {
		return cerrors.Newf(cerrors.NotFound, "statement %s not found", statementID)
	}

	delete(fd.policies[key], statementID)
	m.funcs.Set(functionName, fd)

	return nil
}

// GetPolicy returns the function's (qualifier-scoped) resource-based policy as a
// JSON document, matching the shape the AWS SDK expects (IAM policy with Sid/
// Principal/Action/Resource per statement). Returns NotFound when no policy
// exists for the qualifier.
func (m *Mock) GetPolicy(_ context.Context, functionName, qualifier string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	key := policyKey(qualifier)

	scoped := fd.policies[key]
	if len(scoped) == 0 {
		return "", cerrors.Newf(cerrors.NotFound, "no policy for function %s", functionName)
	}

	// A qualified policy is scoped to the qualified function ARN.
	resource := fd.info.ARN
	if key != latestVersion {
		resource += ":" + key
	}

	statements := make([]map[string]any, 0, len(scoped))
	for _, s := range scoped {
		statements = append(statements, statementJSON(s, resource))
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

// statementJSON renders one resource-policy statement in the IAM shape AWS
// returns, with the principal classified by type (see principalJSON).
func statementJSON(s driver.PermissionStatement, resource string) map[string]any {
	stmt := map[string]any{
		"Sid":       s.StatementID,
		"Effect":    "Allow",
		"Principal": principalJSON(s.Principal),
		"Action":    s.Action,
		"Resource":  resource,
	}

	if s.SourceARN != "" {
		stmt["Condition"] = map[string]any{
			"ArnLike": map[string]string{"AWS:SourceArn": s.SourceARN},
		}
	}

	return stmt
}

// principalJSON renders an AddPermission Principal in the shape AWS uses in the
// resource policy: "*" stays a bare wildcard; a 12-digit account id becomes the
// account's IAM root ARN under "AWS"; an IAM ARN stays under "AWS"; anything
// else (a service domain such as s3.amazonaws.com) goes under "Service".
func principalJSON(principal string) any {
	switch {
	case principal == "*":
		return "*"
	case strings.HasPrefix(principal, "arn:"):
		return map[string]string{"AWS": principal}
	case isAccountID(principal):
		return map[string]string{"AWS": "arn:aws:iam::" + principal + ":root"}
	default:
		return map[string]string{"Service": principal}
	}
}

// isAccountID reports whether s is a 12-digit AWS account id.
func isAccountID(s string) bool {
	if len(s) != accountIDLen {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
