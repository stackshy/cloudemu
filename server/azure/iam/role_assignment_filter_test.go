package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

// listAssignments drains a RoleAssignments ListForScope pager, optionally
// filtered, and returns every principalId seen.
func listAssignmentPrincipals(
	t *testing.T, roleAssigns *armauthorization.RoleAssignmentsClient, scope string,
	opts *armauthorization.RoleAssignmentsClientListForScopeOptions,
) []string {
	t.Helper()

	ctx := context.Background()
	pager := roleAssigns.NewListForScopePager(scope, opts)

	var got []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListForScope.NextPage: %v", err)
		}
		for _, ra := range page.Value {
			got = append(got, getStringPtr(ra.Properties.PrincipalID))
		}
	}

	return got
}

// TestSDKAzureIAMListRoleAssignmentsFilterByPrincipalID confirms
// $filter=principalId eq '{guid}' narrows RoleAssignments.ListForScope to
// just that principal's assignments, matching real Azure — and that a
// different principalId returns none.
func TestSDKAzureIAMListRoleAssignmentsFilterByPrincipalID(t *testing.T) {
	roleAssigns := newClientFactory(t).NewRoleAssignmentsClient()
	ctx := context.Background()

	const (
		principalA = "aaaaaaaa-1111-1111-1111-000000000001"
		principalB = "bbbbbbbb-2222-2222-2222-000000000002"
		principalC = "cccccccc-3333-3333-3333-000000000003" // never assigned
	)

	readerRoleDef := builtInReaderRoleDefinitionID()
	ownerRoleDef := testScope + "/providers/Microsoft.Authorization/roleDefinitions/" + builtInOwnerGUID

	// principalA gets two DISTINCT (role, scope) bindings — a duplicate
	// (principal, role, scope) triple would itself conflict — so the "two
	// assignments for one principal" case exercises a real, valid setup.
	mustCreateAssignment(t, roleAssigns, ctx, "44444444-0000-0000-0000-000000000001", readerRoleDef, principalA)
	mustCreateAssignment(t, roleAssigns, ctx, "55555555-0000-0000-0000-000000000002", ownerRoleDef, principalA)
	mustCreateAssignment(t, roleAssigns, ctx, "66666666-0000-0000-0000-000000000003", readerRoleDef, principalB)

	// Unfiltered list sees all three.
	all := listAssignmentPrincipals(t, roleAssigns, testScope, nil)
	if len(all) != 3 {
		t.Fatalf("unfiltered list returned %d assignments, want 3: %v", len(all), all)
	}

	// $filter=principalId eq principalA returns exactly principalA's two assignments.
	filterA := "principalId eq '" + principalA + "'"
	gotA := listAssignmentPrincipals(t, roleAssigns, testScope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: to.Ptr(filterA)})
	if len(gotA) != 2 {
		t.Fatalf("$filter=principalId eq %q returned %d assignments, want 2: %v", principalA, len(gotA), gotA)
	}
	for _, p := range gotA {
		if p != principalA {
			t.Fatalf("$filter=principalId eq %q leaked assignment for %q", principalA, p)
		}
	}

	// A principal with zero assignments returns an empty (not error) list.
	filterC := "principalId eq '" + principalC + "'"
	gotC := listAssignmentPrincipals(t, roleAssigns, testScope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: to.Ptr(filterC)})
	if len(gotC) != 0 {
		t.Fatalf("$filter=principalId eq %q returned %d assignments, want 0: %v", principalC, len(gotC), gotC)
	}
}

func mustCreateAssignment(
	t *testing.T, roleAssigns *armauthorization.RoleAssignmentsClient, ctx context.Context,
	assignmentID, roleDefID, principalID string,
) {
	t.Helper()

	if _, err := roleAssigns.Create(ctx, testScope, assignmentID,
		assignmentParams(roleDefID, principalID), nil); err != nil {
		t.Fatalf("Create role assignment %s: %v", assignmentID, err)
	}
}

// TestSDKAzureIAMDeleteRoleDefinitionBlockedByActiveAssignment confirms real
// Azure's referential-integrity guard: deleting a custom role definition that
// an active role assignment still references is rejected, and deleting
// succeeds once that assignment is removed.
func TestSDKAzureIAMDeleteRoleDefinitionBlockedByActiveAssignment(t *testing.T) {
	roleDefs, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	const (
		roleDefID    = "77777777-aaaa-bbbb-cccc-000000000001"
		assignmentID = "88888888-aaaa-bbbb-cccc-000000000002"
		principalID  = "99999999-aaaa-bbbb-cccc-000000000003"
	)

	if _, err := roleDefs.CreateOrUpdate(ctx, testScope, roleDefID, armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName: to.Ptr("Deletable Role"),
			RoleType: to.Ptr("CustomRole"),
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate role definition: %v", err)
	}

	roleDefArmID := testScope + "/providers/Microsoft.Authorization/roleDefinitions/" + roleDefID

	if _, err := roleAssigns.Create(ctx, testScope, assignmentID,
		assignmentParams(roleDefArmID, principalID), nil); err != nil {
		t.Fatalf("Create role assignment: %v", err)
	}

	// Deleting the role definition while the assignment still references it
	// must be rejected, not silently succeed and leave a dangling reference.
	_, err := roleDefs.Delete(ctx, testScope, roleDefID, nil)
	if err == nil {
		t.Fatalf("Delete role definition with active assignment: expected error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected *azcore.ResponseError, got %T: %v", err, err)
	}

	if respErr.StatusCode != 409 {
		t.Fatalf("got status %d, want 409", respErr.StatusCode)
	}

	// The role definition must still exist — the rejected delete must not
	// have removed it.
	if _, err := roleDefs.Get(ctx, testScope, roleDefID, nil); err != nil {
		t.Fatalf("Get role definition after blocked delete: %v", err)
	}

	// Remove the assignment, then the delete must succeed.
	if _, err := roleAssigns.Delete(ctx, testScope, assignmentID, nil); err != nil {
		t.Fatalf("Delete role assignment: %v", err)
	}

	if _, err := roleDefs.Delete(ctx, testScope, roleDefID, nil); err != nil {
		t.Fatalf("Delete role definition after removing assignment: %v", err)
	}
}
