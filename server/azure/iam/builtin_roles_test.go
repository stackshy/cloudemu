package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

// The well-known built-in role GUIDs, duplicated here so the test asserts
// against the exact fixed values a real caller would reference.
const (
	builtInReaderGUID      = "acdd72a7-3385-48ef-bd42-f606fba81ae7"
	builtInOwnerGUID       = "8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
	builtInContributorGUID = "b24988ac-6180-42a0-ab88-20f7382dd24c"
)

// TestSDKAzureIAMGetBuiltInRoleByGUID confirms each seeded built-in resolves by
// its fixed GUID at an arbitrary scope, with the expected roleName and the
// BuiltInRole type — the contract IaC tools rely on when they reference
// Owner/Contributor/Reader without first defining a custom role.
func TestSDKAzureIAMGetBuiltInRoleByGUID(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	cases := []struct {
		guid string
		name string
	}{
		{builtInOwnerGUID, "Owner"},
		{builtInContributorGUID, "Contributor"},
		{builtInReaderGUID, "Reader"},
	}

	for _, c := range cases {
		got, err := roleDefs.Get(ctx, testScope, c.guid, nil)
		if err != nil {
			t.Fatalf("Get built-in %s: %v", c.name, err)
		}

		if got.Properties == nil {
			t.Fatalf("Get built-in %s: nil properties", c.name)
		}

		if rn := getStringPtr(got.Properties.RoleName); rn != c.name {
			t.Fatalf("built-in %s: got roleName %q, want %q", c.guid, rn, c.name)
		}

		if rt := getStringPtr(got.Properties.RoleType); rt != "BuiltInRole" {
			t.Fatalf("built-in %s: got roleType %q, want BuiltInRole", c.name, rt)
		}
	}
}

// TestSDKAzureIAMBuiltInRolesListedAtScope confirms the three built-ins appear
// in a RoleDefinitions list at a subscription scope even with no custom roles
// defined, matching real Azure (where built-ins are assignable at every scope).
func TestSDKAzureIAMBuiltInRolesListedAtScope(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	seen := map[string]bool{}

	pager := roleDefs.NewListPager(testScope, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}
		for _, rd := range page.Value {
			seen[getStringPtr(rd.Name)] = true
		}
	}

	for _, guid := range []string{builtInOwnerGUID, builtInContributorGUID, builtInReaderGUID} {
		if !seen[guid] {
			t.Fatalf("built-in role %s missing from list at scope", guid)
		}
	}
}

// TestSDKAzureIAMCreateBuiltInRoleGUIDIsRejected confirms a PUT that reuses a
// built-in role's fixed GUID is rejected with a built-in-protection conflict
// (409), rather than silently creating a colliding custom role definition. A
// follow-up GET must still return the untouched built-in.
func TestSDKAzureIAMCreateBuiltInRoleGUIDIsRejected(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	for _, guid := range []string{builtInOwnerGUID, builtInContributorGUID, builtInReaderGUID} {
		_, err := roleDefs.CreateOrUpdate(ctx, testScope, guid,
			armauthorization.RoleDefinition{
				Properties: &armauthorization.RoleDefinitionProperties{
					RoleName: to.Ptr("hijack"),
					RoleType: to.Ptr("CustomRole"),
				},
			}, nil)
		if err == nil {
			t.Fatalf("PUT on built-in %s: expected error, got nil", guid)
		}

		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) {
			t.Fatalf("built-in %s: expected *azcore.ResponseError, got %T: %v", guid, err, err)
		}

		if respErr.StatusCode != 409 {
			t.Fatalf("built-in %s: got status %d, want 409", guid, respErr.StatusCode)
		}

		if respErr.ErrorCode != "RoleDefinitionUpdateConflict" {
			t.Fatalf("built-in %s: got error code %q, want RoleDefinitionUpdateConflict",
				guid, respErr.ErrorCode)
		}

		// The built-in must remain intact — the rejected PUT must not have
		// overwritten its roleName or flipped its type to CustomRole.
		got, gerr := roleDefs.Get(ctx, testScope, guid, nil)
		if gerr != nil {
			t.Fatalf("Get built-in %s after rejected PUT: %v", guid, gerr)
		}

		if rt := getStringPtr(got.Properties.RoleType); rt != "BuiltInRole" {
			t.Fatalf("built-in %s clobbered by rejected PUT: roleType %q", guid, rt)
		}
	}
}

// TestSDKAzureIAMDeleteBuiltInRoleGUIDIsRejected confirms DELETE on a built-in
// role GUID returns a built-in-protection conflict (409) rather than the 404 a
// bare driver lookup would surface, and leaves the built-in retrievable.
func TestSDKAzureIAMDeleteBuiltInRoleGUIDIsRejected(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	for _, guid := range []string{builtInOwnerGUID, builtInContributorGUID, builtInReaderGUID} {
		_, err := roleDefs.Delete(ctx, testScope, guid, nil)
		if err == nil {
			t.Fatalf("DELETE on built-in %s: expected error, got nil", guid)
		}

		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) {
			t.Fatalf("built-in %s: expected *azcore.ResponseError, got %T: %v", guid, err, err)
		}

		if respErr.StatusCode != 409 {
			t.Fatalf("built-in %s: got status %d, want 409 (not the driver 404)",
				guid, respErr.StatusCode)
		}

		if respErr.ErrorCode != "RoleDefinitionUpdateConflict" {
			t.Fatalf("built-in %s: got error code %q, want RoleDefinitionUpdateConflict",
				guid, respErr.ErrorCode)
		}

		if _, gerr := roleDefs.Get(ctx, testScope, guid, nil); gerr != nil {
			t.Fatalf("built-in %s removed by rejected DELETE: %v", guid, gerr)
		}
	}
}
