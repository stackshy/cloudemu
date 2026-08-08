package aws

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	sagemakerdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// SageMaker models and endpoints created through the mock must surface in the
// cross-service inventory under the sagemaker service.
func TestResourceDiscoverySurfacesSageMaker(t *testing.T) {
	ctx := context.Background()
	p := New()

	if _, err := p.SageMaker.CreateModel(ctx, sagemakerdriver.ModelConfig{
		ModelName: "fraud-model",
		Tags:      []sagemakerdriver.Tag{{Key: "env", Value: "prod"}},
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{
		Services: []string{resourcediscovery.ServiceSageMaker},
	})
	if err != nil {
		t.Fatalf("discovery List: %v", err)
	}

	var found bool
	for _, r := range res {
		if r.Type == resourcediscovery.TypeModel && r.ID == "fraud-model" {
			found = true
			if r.Tags["env"] != "prod" {
				t.Errorf("model tags not surfaced: %+v", r.Tags)
			}
		}
	}

	if !found {
		t.Fatalf("expected SageMaker model in discovery output, got %d resources", len(res))
	}
}
