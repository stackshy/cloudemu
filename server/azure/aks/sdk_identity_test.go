package aks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// TestSDKAKSUserAssignedIdentityRoundTrips asserts a UserAssigned identity is
// echoed on GET with the submitted identity ARM ID and a synthesized
// principalId/clientId per identity. The top-level identity block is not
// covered by the property overlay, so the wire model must carry it.
func TestSDKAKSUserAssignedIdentityRoundTrips(t *testing.T) {
	clusters, _, _ := newSDKClients(t)

	const uamiID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/my-identity"

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Identity: &armcontainerservice.ManagedClusterIdentity{
			Type: to.Ptr(armcontainerservice.ResourceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armcontainerservice.ManagedServiceIdentityUserAssignedIdentitiesValue{
				uamiID: {},
			},
		},
	})

	got, err := clusters.Get(context.Background(), "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity == nil || got.Identity.Type == nil ||
		*got.Identity.Type != armcontainerservice.ResourceIdentityTypeUserAssigned {
		t.Fatalf("identity type: got %+v, want UserAssigned", got.Identity)
	}

	uai := got.Identity.UserAssignedIdentities
	if len(uai) != 1 {
		t.Fatalf("userAssignedIdentities: got %d, want 1", len(uai))
	}

	val, ok := uai[uamiID]
	if !ok || val == nil {
		t.Fatalf("expected identity %q echoed, got %v", uamiID, uai)
	}

	if val.PrincipalID == nil || *val.PrincipalID == "" {
		t.Fatal("expected synthesized principalId on user-assigned identity")
	}

	if val.ClientID == nil || *val.ClientID == "" {
		t.Fatal("expected synthesized clientId on user-assigned identity")
	}
}
