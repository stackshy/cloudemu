package lambda

import (
	"context"
	"encoding/json"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// resolveLayerVersionARN resolves the layer/version a permission operation
// targets, returning the version's ARN (the policy Resource) or a NotFound
// error matching real Lambda's behavior of rejecting a permission call against
// a layer or version that does not exist.
func (m *Mock) resolveLayerVersionARN(name string, version int) (string, error) {
	ld, ok := m.layers.Get(name)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "layer %s not found", name)
	}

	lv, ok := ld.versions.Get(strconv.Itoa(version))
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "layer %s version %d not found", name, version)
	}

	return lv.ARN, nil
}

// validateLayerPermissionStatement checks the required fields of an
// AddLayerVersionPermission statement, matching the fields real Lambda marks
// required on the operation.
func validateLayerPermissionStatement(stmt driver.LayerPermissionStatement) error {
	if stmt.StatementID == "" {
		return cerrors.New(cerrors.InvalidArgument, "StatementId is required")
	}

	if stmt.Action == "" {
		return cerrors.New(cerrors.InvalidArgument, "Action is required")
	}

	if stmt.Principal == "" {
		return cerrors.New(cerrors.InvalidArgument, "Principal is required")
	}

	return nil
}

// AddLayerVersionPermission adds a statement to a layer version's resource-based
// policy, granting another account (or all accounts in an organization, or all
// AWS accounts) permission to use the layer version. ifMatchRevisionID, when
// non-empty, must match the policy's current RevisionId or the call fails
// (AWS's optimistic-concurrency guard against modifying a policy that changed
// since it was last read). Returns the added statement rendered as the JSON
// document AWS echoes back, and the policy's new RevisionId.
//
// Real Lambda enforces layer version permissions on GetLayerVersion; this
// emulator accepts any credentials (see server/README auth note), so the
// policy is stored and returned but never evaluated to deny a caller.
func (m *Mock) AddLayerVersionPermission(
	_ context.Context, name string, version int, stmt driver.LayerPermissionStatement, ifMatchRevisionID string,
) (statementJSON, revisionID string, err error) {
	if verr := validateLayerPermissionStatement(stmt); verr != nil {
		return "", "", verr
	}

	resource, err := m.resolveLayerVersionARN(name, version)
	if err != nil {
		return "", "", err
	}

	ld, _ := m.layers.Get(name)

	ld.mu.Lock()
	defer ld.mu.Unlock()

	pol := ld.permissions[version]
	if pol == nil {
		pol = &layerVersionPolicy{statements: make(map[string]driver.LayerPermissionStatement)}
	}

	if ifMatchRevisionID != "" && pol.revisionID != ifMatchRevisionID {
		return "", "", cerrors.New(cerrors.FailedPrecondition,
			"the RevisionId provided does not match the latest RevisionId for the layer version. "+
				"Call the GetLayerVersionPolicy API to retrieve the latest RevisionId for your resource")
	}

	if _, exists := pol.statements[stmt.StatementID]; exists {
		return "", "", cerrors.Newf(cerrors.AlreadyExists, "statement %s already exists", stmt.StatementID)
	}

	pol.statements[stmt.StatementID] = stmt
	pol.revisionID = newRevisionID()

	if ld.permissions == nil {
		ld.permissions = make(map[int]*layerVersionPolicy)
	}

	ld.permissions[version] = pol

	doc, err := json.Marshal(layerStatementJSON(stmt, resource))
	if err != nil {
		return "", "", err
	}

	return string(doc), pol.revisionID, nil
}

// RemoveLayerVersionPermission drops a statement from a layer version's
// resource-based policy. ifMatchRevisionID, when non-empty, must match the
// policy's current RevisionId or the call fails.
func (m *Mock) RemoveLayerVersionPermission(_ context.Context, name string, version int, statementID, ifMatchRevisionID string) error {
	ld, ok := m.layers.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "layer %s not found", name)
	}

	ld.mu.Lock()
	defer ld.mu.Unlock()

	pol := ld.permissions[version]
	if pol == nil {
		return cerrors.Newf(cerrors.NotFound, "no policy for layer %s version %d", name, version)
	}

	if _, exists := pol.statements[statementID]; !exists {
		return cerrors.Newf(cerrors.NotFound, "statement %s not found", statementID)
	}

	if ifMatchRevisionID != "" && pol.revisionID != ifMatchRevisionID {
		return cerrors.New(cerrors.FailedPrecondition,
			"the RevisionId provided does not match the latest RevisionId for the layer version. "+
				"Call the GetLayerVersionPolicy API to retrieve the latest RevisionId for your resource")
	}

	delete(pol.statements, statementID)
	pol.revisionID = newRevisionID()

	return nil
}

// GetLayerVersionPolicy returns a layer version's resource-based policy as a
// JSON document (the shape the AWS SDK expects: an IAM policy with Sid/
// Principal/Action/Resource per statement), plus its current RevisionId.
// Returns NotFound when the layer version has no policy statements, matching
// real Lambda.
func (m *Mock) GetLayerVersionPolicy(_ context.Context, name string, version int) (policy, revisionID string, err error) {
	resource, err := m.resolveLayerVersionARN(name, version)
	if err != nil {
		return "", "", err
	}

	ld, _ := m.layers.Get(name)

	ld.mu.Lock()
	defer ld.mu.Unlock()

	pol := ld.permissions[version]
	if pol == nil || len(pol.statements) == 0 {
		return "", "", cerrors.Newf(cerrors.NotFound, "no policy for layer %s version %d", name, version)
	}

	statements := make([]map[string]any, 0, len(pol.statements))
	for _, s := range pol.statements {
		statements = append(statements, layerStatementJSON(s, resource))
	}

	doc, err := json.Marshal(map[string]any{
		"Version":   "2012-10-17",
		"Id":        "default",
		"Statement": statements,
	})
	if err != nil {
		return "", "", err
	}

	return string(doc), pol.revisionID, nil
}

// layerStatementJSON renders one layer-version-policy statement in the IAM
// shape AWS returns. An OrganizationId scopes the grant with a
// StringEquals/aws:PrincipalOrgID condition, matching AddLayerVersionPermission
// semantics.
func layerStatementJSON(s driver.LayerPermissionStatement, resource string) map[string]any {
	stmt := map[string]any{
		"Sid":       s.StatementID,
		"Effect":    "Allow",
		"Principal": principalJSON(s.Principal),
		"Action":    s.Action,
		"Resource":  resource,
	}

	if s.OrganizationID != "" {
		stmt["Condition"] = map[string]any{
			"StringEquals": map[string]string{"aws:PrincipalOrgID": s.OrganizationID},
		}
	}

	return stmt
}
