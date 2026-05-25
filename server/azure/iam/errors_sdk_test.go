package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

// armauthorization surfaces handler errors as *azcore.ResponseError. The
// tests in this file lock in the StatusCode contract for each error path so
// regressions on the JSON envelope or the wire status surface immediately.

func TestSDKAzureIAMRoleDefinitionNotFoundIsTyped(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	_, err := roleDefs.Get(ctx, testScope, "missing-role-id", nil)
	if err == nil {
		t.Fatalf("Get on missing role definition: expected error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected *azcore.ResponseError, got %T: %v", err, err)
	}

	if respErr.StatusCode != 404 {
		t.Fatalf("got status %d, want 404", respErr.StatusCode)
	}
}

func TestSDKAzureIAMRoleAssignmentNotFoundIsTyped(t *testing.T) {
	_, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	_, err := roleAssigns.Get(ctx, testScope, "missing-assignment-id", nil)
	if err == nil {
		t.Fatalf("Get on missing role assignment: expected error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected *azcore.ResponseError, got %T: %v", err, err)
	}

	if respErr.StatusCode != 404 {
		t.Fatalf("got status %d, want 404", respErr.StatusCode)
	}
}

func TestSDKAzureIAMCreateAssignmentMissingPrincipalIsRejected(t *testing.T) {
	_, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	_, err := roleAssigns.Create(ctx, testScope, "any-id",
		armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				RoleDefinitionID: to.Ptr(testScope +
					"/providers/Microsoft.Authorization/roleDefinitions/some-role"),
				// PrincipalID omitted on purpose
			},
		}, nil)
	if err == nil {
		t.Fatalf("Create without principalId: expected error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected *azcore.ResponseError, got %T: %v", err, err)
	}

	if respErr.StatusCode != 400 {
		t.Fatalf("got status %d, want 400", respErr.StatusCode)
	}
}

// TestSDKAzureIAMUpdateReplacesStoredValue exercises the create-then-update
// path on a single role definition. Both PUTs must succeed, and a subsequent
// GET must return the second PUT's values — confirming the upsert path in
// CreateOrUpdate actually replaces (rather than silently keeping the
// original). Status code is always 201 for this specific endpoint per the
// Azure REST spec — see the comment in createOrUpdateRoleDefinition.
func TestSDKAzureIAMUpdateReplacesStoredValue(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	const roleID = "11111111-aaaa-bbbb-cccc-222222222222"

	def := armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName: to.Ptr("v1"),
			RoleType: to.Ptr("CustomRole"),
		},
	}

	// First PUT — create.
	if _, err := roleDefs.CreateOrUpdate(ctx, testScope, roleID, def, nil); err != nil {
		t.Fatalf("first CreateOrUpdate: %v", err)
	}

	// Second PUT — update. Round-trip a different role name so we know the
	// update actually replaced the stored value.
	def.Properties.RoleName = to.Ptr("v2")
	if _, err := roleDefs.CreateOrUpdate(ctx, testScope, roleID, def, nil); err != nil {
		t.Fatalf("second CreateOrUpdate (update): %v", err)
	}

	got, err := roleDefs.Get(ctx, testScope, roleID, nil)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if got.Properties == nil || got.Properties.RoleName == nil ||
		*got.Properties.RoleName != "v2" {
		t.Fatalf("update did not replace role: got %+v", got.Properties)
	}
}

// TestSDKAzureIAMDeleteReturnsResource verifies the handler echoes the
// deleted resource back in the body of DELETE — real Azure semantics.
func TestSDKAzureIAMDeleteReturnsResource(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	const roleID = "33333333-dddd-eeee-ffff-444444444444"

	if _, err := roleDefs.CreateOrUpdate(ctx, testScope, roleID,
		armauthorization.RoleDefinition{
			Properties: &armauthorization.RoleDefinitionProperties{
				RoleName: to.Ptr("about-to-be-deleted"),
				RoleType: to.Ptr("CustomRole"),
			},
		}, nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	deleted, err := roleDefs.Delete(ctx, testScope, roleID, nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if deleted.RoleDefinition.Properties == nil ||
		deleted.RoleDefinition.Properties.RoleName == nil ||
		*deleted.RoleDefinition.Properties.RoleName != "about-to-be-deleted" {
		t.Fatalf("Delete did not echo the prior resource: got %+v",
			deleted.RoleDefinition)
	}
}
