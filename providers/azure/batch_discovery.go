package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/batch"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// batchDiscovery projects Azure Batch accounts (Microsoft.Batch/batchAccounts)
// into the cross-service inventory so they surface in Resource Graph /
// `az resource list`. Batch is Azure-only with no shared cross-cloud driver, so
// this rides the generic GenericResources projection (like elasticSanDiscovery)
// rather than a shared walker. Only the top-level accounts are projected; their
// nested pools are child resources.
type batchDiscovery struct{ m *batch.Mock }

func (d batchDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverAccounts(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(a *batch.Account) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if a.ProvisioningState != "" {
			props["provisioningState"] = a.ProvisioningState
		}

		if a.AccountEndpoint != "" {
			props["accountEndpoint"] = a.AccountEndpoint
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceBatch,
			Type:    resourcediscovery.TypeBatchAccount,
			ID:      a.Name,
			ARN:     a.ARMID(),
			Region:  a.Location,
			Tags:    a.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
