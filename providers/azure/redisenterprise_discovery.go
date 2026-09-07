package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/redisenterprise"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// redisEnterpriseDiscovery projects Azure Redis Enterprise clusters
// (Microsoft.Cache/redisEnterprise) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Redis Enterprise is Azure-only
// with no shared cross-cloud driver, so this rides the generic GenericResources
// projection (like elasticSanDiscovery) rather than a shared walker. Only the
// top-level clusters are projected; their nested databases are child resources.
type redisEnterpriseDiscovery struct{ m *redisenterprise.Mock }

func (d redisEnterpriseDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverClusters(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(c *redisenterprise.Cluster) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if c.ProvisioningState != "" {
			props["provisioningState"] = c.ProvisioningState
		}

		if c.HostName != "" {
			props["hostName"] = c.HostName
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceRedisEnterprise,
			Type:    resourcediscovery.TypeRedisEnterprise,
			ID:      c.Name,
			ARN:     c.ARMID(),
			Region:  c.Location,
			Tags:    c.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
