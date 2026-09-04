package azure

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// A user-assigned managed identity created through the mock must surface in the
// cross-service inventory under ServiceIAM / TypeUserAssignedIdentity, so it is
// visible to Resource Graph and `az resource list`. This is the #611 ARG-triple
// wiring — and the collision resolution (it is NOT an AAD user).
func TestResourceDiscoverySurfacesManagedIdentity(t *testing.T) {
	ctx := context.Background()
	p := New()

	id, _, err := p.ManagedIdentity.CreateOrUpdate(ctx, p.SubscriptionID, "rg1", "app-id", managedidentity.Input{
		Location: "eastus",
		Tags:     map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceIAM},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var found bool
	for _, r := range res {
		if r.Type == resourcediscovery.TypeUserAssignedIdentity && r.ID == "app-id" {
			found = true

			if r.ARN != id.ARMID() {
				t.Errorf("ARN = %q, want %q", r.ARN, id.ARMID())
			}

			if r.Tags["env"] != "prod" {
				t.Errorf("tags not surfaced: %+v", r.Tags)
			}
		}
	}

	if !found {
		t.Fatalf("managed identity not found in inventory (%d resources)", len(res))
	}
}
