package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

func builtInReaderRoleDefinitionID() string {
	return testScope + "/providers/Microsoft.Authorization/roleDefinitions/" + builtInReaderGUID
}

func assignmentParams(roleDefID, principalID string) armauthorization.RoleAssignmentCreateParameters {
	return armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: to.Ptr(roleDefID),
			PrincipalID:      to.Ptr(principalID),
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeUser),
		},
	}
}

func statusCode(t *testing.T, err error) int {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected *azcore.ResponseError, got %T: %v", err, err)
	}

	return respErr.StatusCode
}

// TestSDKAzureIAMAssignmentDanglingRoleDefinitionRejected confirms creating a
// role assignment that references a non-existent role definition fails with a
// 400 — real Azure's referential-integrity guard (RoleDefinitionDoesNotExist).
func TestSDKAzureIAMAssignmentDanglingRoleDefinitionRejected(t *testing.T) {
	_, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	danglingRoleDef := testScope +
		"/providers/Microsoft.Authorization/roleDefinitions/99999999-0000-0000-0000-000000000000"

	_, err := roleAssigns.Create(ctx, testScope, "eeeeeeee-0000-0000-0000-000000000001",
		assignmentParams(danglingRoleDef, "ffffffff-0000-0000-0000-000000000002"), nil)
	if err == nil {
		t.Fatalf("Create with dangling roleDefinitionId: expected error, got nil")
	}

	if got := statusCode(t, err); got != 400 {
		t.Fatalf("got status %d, want 400", got)
	}
}

// TestSDKAzureIAMDuplicateAssignmentGUIDConflicts confirms re-creating the same
// assignment GUID conflicts with 409 (RoleAssignmentExists), rather than
// silently overwriting.
func TestSDKAzureIAMDuplicateAssignmentGUIDConflicts(t *testing.T) {
	_, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	const assignmentID = "11111111-2222-3333-4444-555555555555"

	params := assignmentParams(builtInReaderRoleDefinitionID(), "aaaaaaaa-0000-0000-0000-000000000001")

	if _, err := roleAssigns.Create(ctx, testScope, assignmentID, params, nil); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := roleAssigns.Create(ctx, testScope, assignmentID, params, nil)
	if err == nil {
		t.Fatalf("re-create of same assignment GUID: expected error, got nil")
	}

	if got := statusCode(t, err); got != 409 {
		t.Fatalf("got status %d, want 409", got)
	}
}

// TestSDKAzureIAMDuplicateAssignmentTripleConflicts confirms a second, distinct
// assignment GUID binding the same (principal, role, scope) triple conflicts
// with 409 — matching real Azure, which rejects duplicate bindings.
func TestSDKAzureIAMDuplicateAssignmentTripleConflicts(t *testing.T) {
	_, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	const principalID = "bbbbbbbb-0000-0000-0000-000000000002"

	params := assignmentParams(builtInReaderRoleDefinitionID(), principalID)

	if _, err := roleAssigns.Create(ctx, testScope,
		"22222222-0000-0000-0000-000000000001", params, nil); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Different assignment GUID, same principal/role/scope.
	_, err := roleAssigns.Create(ctx, testScope,
		"33333333-0000-0000-0000-000000000002", params, nil)
	if err == nil {
		t.Fatalf("duplicate (principal, role, scope) triple: expected error, got nil")
	}

	if got := statusCode(t, err); got != 409 {
		t.Fatalf("got status %d, want 409", got)
	}
}
