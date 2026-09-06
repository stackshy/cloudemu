package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/signalr"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// signalRDiscovery projects Azure SignalR Service resources
// (Microsoft.SignalRService/signalR) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. SignalR is Azure-only with no
// shared cross-cloud driver, so this rides the generic GenericResources
// projection (like loadTestDiscovery) rather than a shared walker.
type signalRDiscovery struct{ m *signalr.Mock }

func (d signalRDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverSignalR(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *signalr.SignalR) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSignalR,
			Type:    resourcediscovery.TypeSignalR,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: signalRProps(s)},
		}
	}), nil
}

// signalRProps projects a signalR resource's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func signalRProps(s *signalr.SignalR) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
