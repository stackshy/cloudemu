package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/chaosstudio"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// chaosStudioDiscovery projects Azure Chaos Studio resources
// (Microsoft.Chaos/experiments) into the cross-service inventory so they surface
// in Resource Graph / `az resource list`. Chaos Studio is Azure-only with no
// shared cross-cloud driver, so this rides the generic GenericResources
// projection (like devCenterDiscovery) rather than a shared walker.
type chaosStudioDiscovery struct{ m *chaosstudio.Mock }

func (d chaosStudioDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverExperiments(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *chaosstudio.Experiment) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceChaosStudio,
			Type:    resourcediscovery.TypeChaosExperiment,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: chaosStudioProps(s)},
		}
	}), nil
}

// chaosStudioProps projects an experiment's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func chaosStudioProps(s *chaosstudio.Experiment) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
