package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/digitaltwins"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// digitalTwinsDiscovery projects Azure Digital Twins resources
// (Microsoft.DigitalTwins/digitalTwinsInstances) into the cross-service
// inventory so they surface in Resource Graph / `az resource list`. Digital
// Twins is Azure-only with no shared cross-cloud driver, so this rides the
// generic GenericResources projection (like signalRDiscovery) rather than a
// shared walker.
type digitalTwinsDiscovery struct{ m *digitaltwins.Mock }

func (d digitalTwinsDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverInstances(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(i *digitaltwins.Instance) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceDigitalTwins,
			Type:    resourcediscovery.TypeDigitalTwinsInstance,
			ID:      i.Name,
			ARN:     i.ARMID(),
			Region:  i.Location,
			Tags:    i.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: digitalTwinsProps(i)},
		}
	}), nil
}

// digitalTwinsProps projects a Digital Twins instance's provisioning state into
// its inventory row's properties bag. Returns nil when unset so an empty
// properties block is omitted.
func digitalTwinsProps(i *digitaltwins.Instance) map[string]any {
	if i.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": i.ProvisioningState}
}
