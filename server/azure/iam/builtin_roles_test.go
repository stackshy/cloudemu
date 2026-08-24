package iam_test

import (
	"context"
	"testing"
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
