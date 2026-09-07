package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/communication"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// communicationDiscovery projects Azure Communication Services resources
// (Microsoft.Communication/communicationServices) into the cross-service
// inventory so they surface in Resource Graph / `az resource list`. Communication
// Services is Azure-only with no shared cross-cloud driver, so this rides the
// generic GenericResources projection (like signalRDiscovery) rather than a
// shared walker.
type communicationDiscovery struct{ m *communication.Mock }

func (d communicationDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverCommunication(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *communication.Communication) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceCommunication,
			Type:    resourcediscovery.TypeCommunicationService,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: communicationProps(s)},
		}
	}), nil
}

// communicationProps projects a communicationServices resource's provisioning
// state into its inventory row's properties bag. Returns nil when unset so an
// empty properties block is omitted.
func communicationProps(s *communication.Communication) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
