package gcp

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	vertexaidriver "github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// vertexDiscovery projects the durable Vertex AI resources (endpoints and
// datasets) into the cross-service inventory. An empty location lists across
// all regions.
type vertexDiscovery struct{ m vertexMock }

// vertexMock is the subset of the Vertex AI mock discovery reads.
type vertexMock interface {
	ListEndpoints(ctx context.Context, location string) ([]vertexaidriver.Endpoint, error)
	ListDatasets(ctx context.Context, location string) ([]vertexaidriver.Dataset, error)
}

func vertexID(displayName, name string) string {
	if displayName != "" {
		return displayName
	}

	return name
}

func (a vertexDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	endpoints, err := a.m.ListEndpoints(ctx, "")
	if err != nil {
		return nil, err
	}

	datasets, err := a.m.ListDatasets(ctx, "")
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredResource, 0, len(endpoints)+len(datasets))

	for i := range endpoints {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceVertexAI, Type: resourcediscovery.TypeEndpoint,
			ID:   vertexID(endpoints[i].DisplayName, endpoints[i].Name),
			ARN:  endpoints[i].Name,
			Tags: endpoints[i].Labels,
		})
	}

	for i := range datasets {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceVertexAI, Type: resourcediscovery.TypeDataset,
			ID:   vertexID(datasets[i].DisplayName, datasets[i].Name),
			ARN:  datasets[i].Name,
			Tags: datasets[i].Labels,
		})
	}

	return out, nil
}
