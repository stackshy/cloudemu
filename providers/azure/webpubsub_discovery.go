package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/webpubsub"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// webPubSubDiscovery projects Azure Web PubSub Service resources
// (Microsoft.SignalRService/webPubSub) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Web PubSub is Azure-only with
// no shared cross-cloud driver, so this rides the generic GenericResources
// projection (like signalRDiscovery) rather than a shared walker.
//
//nolint:dupl // parallel-shaped to signalr_discovery.go, distinct resource type
type webPubSubDiscovery struct{ m *webpubsub.Mock }

func (d webPubSubDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverWebPubSub(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *webpubsub.WebPubSub) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceWebPubSub,
			Type:    resourcediscovery.TypeWebPubSub,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: webPubSubProps(s)},
		}
	}), nil
}

// webPubSubProps projects a webPubSub resource's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func webPubSubProps(s *webpubsub.WebPubSub) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
