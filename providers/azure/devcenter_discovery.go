package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/devcenter"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// devCenterDiscovery projects Azure Dev Center resources
// (Microsoft.DevCenter/devcenters) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Dev Center is Azure-only with
// no shared cross-cloud driver, so this rides the generic GenericResources
// projection (like signalRDiscovery) rather than a shared walker.
type devCenterDiscovery struct{ m *devcenter.Mock }

func (d devCenterDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverDevCenters(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *devcenter.DevCenter) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceDevCenter,
			Type:    resourcediscovery.TypeDevCenter,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: devCenterProps(s)},
		}
	}), nil
}

// devCenterProps projects a dev center resource's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func devCenterProps(s *devcenter.DevCenter) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
