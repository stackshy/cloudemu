package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/loadtesting"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// loadTestDiscovery projects Azure Load Testing resources
// (Microsoft.LoadTestService/loadTests) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Load tests are Azure-only with
// no shared cross-cloud driver, so this rides the generic GenericResources
// projection (like managedIdentityDiscovery) rather than a shared walker.
type loadTestDiscovery struct{ m *loadtesting.Mock }

func (d loadTestDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	tests, err := d.m.DiscoverLoadTests(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(tests, func(lt *loadtesting.LoadTest) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceLoadTesting,
			Type:    resourcediscovery.TypeLoadTest,
			ID:      lt.Name,
			ARN:     lt.ARMID(),
			Region:  lt.Location,
			Tags:    lt.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: loadTestProps(lt)},
		}
	}), nil
}

// loadTestProps projects a load test's provisioning state into its inventory
// row's properties bag, matching the provisioningState a real Resource Graph row
// carries. Returns nil when unset so an empty properties block is omitted.
func loadTestProps(lt *loadtesting.LoadTest) map[string]any {
	if lt.ProvisioningState == "" {
		return nil
	}

	return map[string]any{"provisioningState": lt.ProvisioningState}
}
