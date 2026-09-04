package resourcegraph

import "testing"

// TestManagedIdentityTypeMapping locks in the #611 collision resolution:
// microsoft.managedidentity/userassignedidentities now maps to the real
// managed-identity resource (iam/UserAssignedIdentity), NOT to AAD users
// (iam/User) as it did before. AAD users keep their own, non-ARM label.
func TestManagedIdentityTypeMapping(t *testing.T) {
	const armIdentity = "microsoft.managedidentity/userassignedidentities"

	// Forward: KQL `where type == '<armIdentity>'` resolves to the managed
	// identity portable pair.
	if svc, typ := mapAzureType(armIdentity); svc != "iam" || typ != "UserAssignedIdentity" {
		t.Errorf("mapAzureType(%q) = (%q,%q), want (iam,UserAssignedIdentity)", armIdentity, svc, typ)
	}

	// Reverse: a discovered managed identity stamps the real ARM type.
	if got := portableToAzureType("iam", "UserAssignedIdentity"); got != armIdentity {
		t.Errorf("portableToAzureType(iam,UserAssignedIdentity) = %q, want %q", got, armIdentity)
	}

	// AAD users no longer collide with the managed-identity type — they fall back
	// to their own lowercased label.
	if got := portableToAzureType("iam", "User"); got == armIdentity {
		t.Errorf("iam/User still maps to %q — collision not resolved", armIdentity)
	}
}
