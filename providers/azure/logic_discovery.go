package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// logicWorkflowDiscovery projects Azure Logic Apps (Consumption) workflows
// (Microsoft.Logic/workflows) into the cross-service inventory so they surface in
// Resource Graph / `az resource list`. Logic Apps is Azure-only with no shared
// cross-cloud driver, so this rides the generic GenericResources projection
// (like loadTestDiscovery) rather than a shared walker.
type logicWorkflowDiscovery struct{ m *logic.Mock }

func (d logicWorkflowDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	workflows, err := d.m.DiscoverWorkflows(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(workflows, func(wf *logic.Workflow) resourcediscovery.DiscoveredResource {
		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceLogic,
			Type:    resourcediscovery.TypeLogicWorkflow,
			ID:      wf.Name,
			ARN:     wf.ARMID(),
			Region:  wf.Location,
			Tags:    wf.Tags,
			Attrs: resourcediscovery.Attributes{Properties: map[string]any{
				"provisioningState": wf.ProvisioningState,
				"state":             wf.State,
			}},
		}
	}), nil
}
