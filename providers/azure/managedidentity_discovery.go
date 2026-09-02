package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// managedIdentityDiscovery projects user-assigned managed identities
// (Microsoft.ManagedIdentity/userAssignedIdentities) into the cross-service
// inventory so they surface in Resource Graph / `az resource list`. Managed
// identities are Azure-only with no shared cross-cloud driver, so this rides the
// generic GenericResources projection (like azureMLDiscovery) rather than a
// shared walker.
type managedIdentityDiscovery struct{ m *managedidentity.Mock }

func (d managedIdentityDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	ids, err := d.m.DiscoverIdentities(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(ids, func(id *managedidentity.Identity) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceIAM,
			Type:    resourcediscovery.TypeUserAssignedIdentity,
			ID:      id.Name,
			ARN:     id.ARMID(),
			Region:  id.Location,
			Tags:    id.Tags,
		}
	}), nil
}
