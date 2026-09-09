package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/recoveryservices"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// recoveryServicesDiscovery projects Azure Recovery Services vaults
// (Microsoft.RecoveryServices/vaults) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Recovery Services is
// Azure-only with no shared cross-cloud driver, so this rides the generic
// GenericResources projection (like streamAnalyticsDiscovery) rather than a
// shared walker. Only the top-level vaults are projected; their backup policies
// and configs are child resources.
type recoveryServicesDiscovery struct{ m *recoveryservices.Mock }

func (d recoveryServicesDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverVaults(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(v *recoveryservices.Vault) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if v.ProvisioningState != "" {
			props["provisioningState"] = v.ProvisioningState
		}

		if v.SkuName != "" {
			props["sku"] = v.SkuName
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceRecoveryServices,
			Type:    resourcediscovery.TypeRecoveryVault,
			ID:      v.Name,
			ARN:     v.ARMID(),
			Region:  v.Location,
			Tags:    v.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
