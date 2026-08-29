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

// smMock is the subset of the SageMaker mock discovery reads. ListTags reads
// the authoritative ARN-keyed tag store (the target of AddTags / the Resource
// Groups Tagging API), which is seeded with create-time tags too — so discovery
// reflects tags applied through either path rather than the stale struct copy.
type smMock interface {
	ListModels(ctx context.Context) ([]sagemakerdriver.Model, error)
	ListEndpoints(ctx context.Context) ([]sagemakerdriver.Endpoint, error)
	ListNotebookInstances(ctx context.Context) ([]sagemakerdriver.NotebookInstance, error)
	ListTags(ctx context.Context, resourceARN string) ([]sagemakerdriver.Tag, error)
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
		tags, err := a.liveTags(ctx, models[i].ModelARN)
		if err != nil {
			return nil, err
		}

		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSageMaker, Type: resourcediscovery.TypeModel,
			ID: models[i].ModelName, ARN: models[i].ModelARN, Tags: tags,
		})
	}

	for i := range endpoints {
		tags, err := a.liveTags(ctx, endpoints[i].EndpointARN)
		if err != nil {
			return nil, err
		}

		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSageMaker, Type: resourcediscovery.TypeEndpoint,
			ID: endpoints[i].EndpointName, ARN: endpoints[i].EndpointARN, Tags: tags,
		})
	}

	for i := range notebooks {
		tags, err := a.liveTags(ctx, notebooks[i].ARN)
		if err != nil {
			return nil, err
		}

		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceSageMaker, Type: resourcediscovery.TypeNotebookInstance,
			ID: notebooks[i].Name, ARN: notebooks[i].ARN, Tags: tags,
		})
	}

	return out, nil
}

// liveTags reads a resource's current tags from the ARN-keyed tag store.
func (a sagemakerDiscovery) liveTags(ctx context.Context, arn string) (map[string]string, error) {
	tags, err := a.m.ListTags(ctx, arn)
	if err != nil {
		return nil, err
	}

	return smTags(tags), nil
}
