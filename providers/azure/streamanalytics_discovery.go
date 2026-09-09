package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/streamanalytics"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// streamAnalyticsDiscovery projects Azure Stream Analytics jobs
// (Microsoft.StreamAnalytics/streamingjobs) into the cross-service inventory so
// they surface in Resource Graph / `az resource list`. Stream Analytics is
// Azure-only with no shared cross-cloud driver, so this rides the generic
// GenericResources projection (like batchDiscovery) rather than a shared walker.
// Only the top-level jobs are projected; their transformations/inputs/outputs/
// functions are child resources.
type streamAnalyticsDiscovery struct{ m *streamanalytics.Mock }

func (d streamAnalyticsDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	items, err := d.m.DiscoverJobs(ctx)
	if err != nil {
		return nil, err
	}

	return projectDiscovery(items, func(j *streamanalytics.StreamingJob) resourcediscovery.DiscoveredResource {
		props := map[string]any{}
		if j.ProvisioningState != "" {
			props["provisioningState"] = j.ProvisioningState
		}

		if j.JobState != "" {
			props["jobState"] = j.JobState
		}

		return resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceStreamAnalytics,
			Type:    resourcediscovery.TypeStreamingJob,
			ID:      j.Name,
			ARN:     j.ARMID(),
			Region:  j.Location,
			Tags:    j.Tags,
			Attrs:   resourcediscovery.Attributes{Properties: props},
		}
	}), nil
}
