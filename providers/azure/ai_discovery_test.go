package azure

import (
	"context"
	"testing"

	aidriver "github.com/stackshy/cloudemu/v2/services/azureai/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// An Azure ML workspace created through the mock must surface in the
// cross-service inventory under the machinelearningservices service, mirroring
// AWS SageMaker and GCP Vertex AI.
func TestResourceDiscoverySurfacesAzureML(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, err := p.AI.CreateMLWorkspace(ctx, aidriver.MLWorkspaceConfig{
		Name: "ml-hub", ResourceGroup: "rg1", Location: "eastus",
		Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateMLWorkspace: %v", err)
	}

	if _, err := p.AI.CreateEndpoint(ctx, aidriver.EndpointConfig{
		Workspace: "ml-hub", ResourceGroup: "rg1", Name: "score-ep", Kind: "online",
	}); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceAzureML},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var found, foundEndpoint bool
	for _, r := range res {
		if r.Type == resourcediscovery.TypeWorkspace && r.ID == "ml-hub" {
			found = true
			if r.Tags["env"] != "prod" {
				t.Errorf("workspace tags not surfaced: %+v", r.Tags)
			}
		}

		if r.Type == resourcediscovery.TypeEndpoint && r.ID == "score-ep" {
			foundEndpoint = true
		}
	}

	if !foundEndpoint {
		t.Errorf("expected Azure ML online endpoint in discovery output")
	}

	if !found {
		t.Fatalf("expected Azure ML workspace in discovery output, got %d resources", len(res))
	}
}
