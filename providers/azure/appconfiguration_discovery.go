package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/appconfiguration"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// appConfigurationDiscovery projects Azure App Configuration resources
// (Microsoft.AppConfiguration/configurationStores) into the cross-service
// inventory so they surface in Resource Graph / `az resource list`. App
// Configuration is Azure-only with no shared cross-cloud driver, so this rides
// the generic GenericResources projection (like signalRDiscovery) rather than a
// shared walker.
type appConfigurationDiscovery struct{ m *appconfiguration.Mock }

func (d appConfigurationDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverConfigurationStores(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *appconfiguration.ConfigurationStore) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceAppConfiguration,
			Type:    resourcediscovery.TypeConfigurationStore,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: appConfigurationProps(s)},
		}
	}), nil
}

// appConfigurationProps projects a configuration store's provisioning state into
// its inventory row's properties bag. Returns nil when unset so an empty
// properties block is omitted.
func appConfigurationProps(s *appconfiguration.ConfigurationStore) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
