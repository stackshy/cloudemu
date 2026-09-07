package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/managedgrafana"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// managedGrafanaDiscovery projects Azure Managed Grafana resources
// (Microsoft.Dashboard/grafana) into the cross-service inventory so they surface
// in Resource Graph / `az resource list`. Managed Grafana is Azure-only with no
// shared cross-cloud driver, so this rides the generic GenericResources
// projection (like signalRDiscovery) rather than a shared walker.
type managedGrafanaDiscovery struct{ m *managedgrafana.Mock }

func (d managedGrafanaDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverGrafana(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *managedgrafana.Grafana) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceManagedGrafana,
			Type:    resourcediscovery.TypeGrafana,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: managedGrafanaProps(s)},
		}
	}), nil
}

// managedGrafanaProps projects a grafana resource's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func managedGrafanaProps(s *managedgrafana.Grafana) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
