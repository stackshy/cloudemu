package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/mongocluster"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// mongoClusterDiscovery projects Azure Cosmos DB for MongoDB (vCore) clusters
// (Microsoft.DocumentDB/mongoClusters) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Mongo clusters are Azure-only
// with no shared cross-cloud driver, so this rides the generic GenericResources
// projection (like redisEnterpriseDiscovery) rather than a shared walker.
type mongoClusterDiscovery struct{ m *mongocluster.Mock }

func (d mongoClusterDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverClusters(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(c *mongocluster.Cluster) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if c.ProvisioningState != "" {
			props["provisioningState"] = c.ProvisioningState
		}

		if c.ClusterStatus != "" {
			props["clusterStatus"] = c.ClusterStatus
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceMongoCluster,
			Type:    resourcediscovery.TypeMongoCluster,
			ID:      c.Name,
			ARN:     c.ARMID(),
			Region:  c.Location,
			Tags:    c.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
