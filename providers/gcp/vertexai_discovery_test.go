package gcp

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	vertexaidriver "github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// A Vertex AI endpoint created through the mock must surface in the
// cross-service inventory under the aiplatform service.
func TestResourceDiscoverySurfacesVertexAI(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, _, err := p.VertexAI.CreateEndpoint(ctx, vertexaidriver.EndpointConfig{
		Location:    "us-central1",
		DisplayName: "reco-endpoint",
		Labels:      map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceVertexAI},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var found bool
	for _, r := range res {
		if r.Type == resourcediscovery.TypeEndpoint && r.ID == "reco-endpoint" {
			found = true
			if r.Tags["env"] != "prod" {
				t.Errorf("endpoint labels not surfaced: %+v", r.Tags)
			}
		}
	}

	if !found {
		t.Fatalf("expected Vertex AI endpoint in discovery output, got %d resources", len(res))
	}
}
