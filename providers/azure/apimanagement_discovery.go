package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/apimanagement"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// propProvisioningState is the discovery attribute carrying a resource's ARM
// provisioning state.
const propProvisioningState = "provisioningState"

// apiManagementDiscovery projects Azure API Management services
// (Microsoft.ApiManagement/service) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. API Management is Azure-only
// with no shared cross-cloud driver, so this rides the generic projection (like
// recoveryServicesDiscovery) rather than a shared walker.
type apiManagementDiscovery struct{ m *apimanagement.Mock }

func (d apiManagementDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverServices(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *apimanagement.Service) resourcediscovery.DiscoveredResource {
		props := map[string]any{
			propProvisioningState: s.ProvisioningState,
			"gatewayUrl":          s.Endpoints().Gateway,
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceAPIManagement,
			Type:    resourcediscovery.TypeAPIManagementService,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs: resourcediscovery.Attributes{
				SKU:         s.SkuName,
				SKUCapacity: int(s.SkuCapacity),
				Zones:       append([]string(nil), s.Zones...),
				Properties:  props,
			},
		}
	}), nil
}
