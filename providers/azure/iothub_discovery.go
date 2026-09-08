package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// iotHubDiscovery projects Azure IoT hubs (Microsoft.Devices/IotHubs) into the
// cross-service inventory so they surface in Resource Graph / `az resource list`.
// IoT Hub is Azure-only with no shared cross-cloud driver, so this rides the
// generic GenericResources projection (like streamAnalyticsDiscovery) rather than
// a shared walker. Only the top-level hubs are projected; their consumer groups
// are child resources.
type iotHubDiscovery struct{ m *iothub.Mock }

func (d iotHubDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverHubs(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(h *iothub.Hub) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if h.ProvisioningState != "" {
			props["provisioningState"] = h.ProvisioningState
		}

		if h.State != "" {
			props["state"] = h.State
		}

		if h.HostName != "" {
			props["hostName"] = h.HostName
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceIoTHub,
			Type:    resourcediscovery.TypeIoTHub,
			ID:      h.Name,
			ARN:     h.ARMID(),
			Region:  h.Location,
			Tags:    h.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
