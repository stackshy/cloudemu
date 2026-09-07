package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/purview"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// purviewDiscovery projects Microsoft Purview resources
// (Microsoft.Purview/accounts) into the cross-service inventory so they surface
// in Resource Graph / `az resource list`. Purview is Azure-only with no shared
// cross-cloud driver, so this rides the generic GenericResources projection
// (like devCenterDiscovery) rather than a shared walker.
type purviewDiscovery struct{ m *purview.Mock }

func (d purviewDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverAccounts(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *purview.Account) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServicePurview,
			Type:    resourcediscovery.TypePurviewAccount,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: purviewProps(s)},
		}
	}), nil
}

// purviewProps projects a Purview account's provisioning state into its
// inventory row's properties bag. Returns nil when unset so an empty properties
// block is omitted.
func purviewProps(s *purview.Account) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
