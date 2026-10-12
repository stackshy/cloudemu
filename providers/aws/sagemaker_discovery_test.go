package aws

import (
	"context"
	"strings"
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

// TestDiscovery_NewTypes pins that the standing taggable SageMaker families
// surface in the inventory with their live tags, and that transient runs do not.
func TestDiscovery_NewTypes(t *testing.T) { // T21
	ctx := context.Background()
	p := New()
	tags := []sagemakerdriver.Tag{{Key: "env", Value: "prod"}}

	mustNoErr := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}

	_, err := p.SageMaker.CreateModel(ctx, sagemakerdriver.ModelConfig{ModelName: "mdl"})
	mustNoErr(err)
	_, err = p.SageMaker.CreateEndpointConfig(ctx, sagemakerdriver.EndpointConfigSpec{
		ConfigName: "cfg", Tags: tags,
		ProductionVariants: []sagemakerdriver.ProductionVariant{{VariantName: "v", ModelName: "mdl", InstanceType: "ml.m5.large", InitialInstanceCount: 1, InitialVariantWeight: 1}},
	})
	mustNoErr(err)
	_, err = p.SageMaker.CreatePipeline(ctx, sagemakerdriver.PipelineSpec{PipelineName: "pl", Tags: tags})
	mustNoErr(err)
	_, err = p.SageMaker.CreateModelPackageGroup(ctx, sagemakerdriver.ModelPackageGroupSpec{GroupName: "grp", Tags: tags})
	mustNoErr(err)
	_, err = p.SageMaker.CreateImage(ctx, sagemakerdriver.ImageSpec{Name: "img", RoleARN: "arn:aws:iam::123456789012:role/r", Tags: tags})
	mustNoErr(err)
	_, err = p.SageMaker.CreateTrainingJob(ctx, sagemakerdriver.TrainingJobConfig{JobName: "tj", Tags: tags})
	mustNoErr(err)

	res, err := p.ResourceDiscovery.List(ctx, resourcediscovery.Query{Services: []string{resourcediscovery.ServiceSageMaker}})
	mustNoErr(err)

	got := map[string]map[string]string{}
	for _, r := range res {
		got[r.Type+"/"+r.ID] = r.Tags
	}

	for _, key := range []string{
		resourcediscovery.TypeEndpointConfig + "/cfg", resourcediscovery.TypePipeline + "/pl",
		resourcediscovery.TypeModelPackageGroup + "/grp", resourcediscovery.TypeMLImage + "/img",
	} {
		tg, ok := got[key]
		if !ok {
			t.Fatalf("%s missing from discovery: %v", key, got)
		}

		if tg["env"] != "prod" {
			t.Errorf("%s tags = %v, want env=prod", key, tg)
		}
	}

	for key := range got {
		if strings.Contains(key, "tj") {
			t.Errorf("transient training job surfaced in discovery: %s", key)
		}
	}
}
