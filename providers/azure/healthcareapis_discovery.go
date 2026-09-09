package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/healthcareapis"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// healthcareApisDiscovery projects Azure Health Data Services workspaces
// (Microsoft.HealthcareApis/workspaces) into the cross-service inventory so they
// surface in Resource Graph / `az resource list`. Health Data Services is
// Azure-only with no shared cross-cloud driver, so this rides the generic
// GenericResources projection (like redisEnterpriseDiscovery). Only the top-level
// workspaces are projected; their nested FHIR and DICOM services are child
// resources.
type healthcareApisDiscovery struct{ m *healthcareapis.Mock }

func (d healthcareApisDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(w *healthcareapis.Workspace) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if w.ProvisioningState != "" {
			props["provisioningState"] = w.ProvisioningState
		}

		if w.PublicNetworkAccess != "" {
			props["publicNetworkAccess"] = w.PublicNetworkAccess
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceHealthcareApis,
			Type:    resourcediscovery.TypeHealthcareWorkspace,
			ID:      w.Name,
			ARN:     w.ARMID(),
			Region:  w.Location,
			Tags:    w.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
