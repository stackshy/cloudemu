package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedlustre"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// managedLustreDiscovery projects Azure Managed Lustre resources
// (Microsoft.StorageCache/amlFilesystems) into the cross-service inventory so
// they surface in Resource Graph / `az resource list`. Managed Lustre is
// Azure-only with no shared cross-cloud driver, so this rides the generic
// GenericResources projection (like elasticSanDiscovery) rather than a shared
// walker.
type managedLustreDiscovery struct{ m *managedlustre.Mock }

func (d managedLustreDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverAmlFilesystems(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *managedlustre.AmlFilesystem) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceManagedLustre,
			Type:    resourcediscovery.TypeAmlFilesystem,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: managedLustreProps(s)},
		}
	}), nil
}

// managedLustreProps projects an amlFilesystem's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func managedLustreProps(s *managedlustre.AmlFilesystem) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
