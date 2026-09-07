package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/elasticsan"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// elasticSanDiscovery projects Azure Elastic SAN resources
// (Microsoft.ElasticSan/elasticSans) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Elastic SAN is Azure-only with
// no shared cross-cloud driver, so this rides the generic GenericResources
// projection (like purviewDiscovery) rather than a shared walker.
type elasticSanDiscovery struct{ m *elasticsan.Mock }

func (d elasticSanDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverElasticSans(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(s *elasticsan.ElasticSan) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceElasticSan,
			Type:    resourcediscovery.TypeElasticSan,
			ID:      s.Name,
			ARN:     s.ARMID(),
			Region:  s.Location,
			Tags:    s.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: elasticSanProps(s)},
		}
	}), nil
}

// elasticSanProps projects an Elastic SAN's provisioning state into its inventory
// row's properties bag. Returns nil when unset so an empty properties block is
// omitted.
func elasticSanProps(s *elasticsan.ElasticSan) map[string]any {
	if s.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": s.ProvisioningState}
}
