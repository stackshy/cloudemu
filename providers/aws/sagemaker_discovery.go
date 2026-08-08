package aws

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	sagemakerdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// sagemakerDiscovery projects the durable, inventory-relevant SageMaker
// resources (models, endpoints, notebook instances) into the cross-service
// inventory. Transient jobs (training/processing/etc.) are intentionally
// excluded — real inventory APIs surface the standing resources, not job runs.
type sagemakerDiscovery struct{ m smMock }

// smMock is the subset of the SageMaker mock discovery reads.
type smMock interface {
	ListModels(ctx context.Context) ([]sagemakerdriver.Model, error)
	ListEndpoints(ctx context.Context) ([]sagemakerdriver.Endpoint, error)
	ListNotebookInstances(ctx context.Context) ([]sagemakerdriver.NotebookInstance, error)
}

func smTags(tags []sagemakerdriver.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func (a sagemakerDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	models, err := a.m.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	endpoints, err := a.m.ListEndpoints(ctx)
	if err != nil {
		return nil, err
	}

	notebooks, err := a.m.ListNotebookInstances(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredResource, 0,
		len(models)+len(endpoints)+len(notebooks))

	for i := range models {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSageMaker, Type: resourcediscovery.TypeModel,
			ID: models[i].ModelName, ARN: models[i].ModelARN, Tags: smTags(models[i].Tags),
		})
	}

	for i := range endpoints {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSageMaker, Type: resourcediscovery.TypeEndpoint,
			ID: endpoints[i].EndpointName, ARN: endpoints[i].EndpointARN, Tags: smTags(endpoints[i].Tags),
		})
	}

	for i := range notebooks {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSageMaker, Type: resourcediscovery.TypeNotebookInstance,
			ID: notebooks[i].Name, ARN: notebooks[i].ARN, Tags: smTags(notebooks[i].Tags),
		})
	}

	return out, nil
}
