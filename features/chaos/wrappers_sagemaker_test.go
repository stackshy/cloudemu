package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/chaos"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

func newChaosSageMaker(t *testing.T) (driver.Service, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapSageMaker(cloudemu.NewAWS().SageMaker, e), e
}

func TestWrapSageMakerCreateTrainingJobChaos(t *testing.T) {
	sm, e := newChaosSageMaker(t)
	ctx := context.Background()

	if _, err := sm.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "j1"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("sagemaker", time.Hour))

	if _, err := sm.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "j2"}); err == nil {
		t.Error("expected chaos error on CreateTrainingJob")
	}
}

func TestWrapSageMakerInvokeEndpointChaos(t *testing.T) {
	sm, e := newChaosSageMaker(t)
	ctx := context.Background()

	_, _ = sm.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	_, _ = sm.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName:         "cfg",
		ProductionVariants: []driver.ProductionVariant{{VariantName: "v1", ModelName: "m"}},
	})
	if _, err := sm.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: "cfg"}); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	if _, err := sm.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep", Body: []byte("x")}); err != nil {
		t.Fatalf("baseline invoke: %v", err)
	}

	e.Apply(chaos.ServiceOutage("sagemaker", time.Hour))

	if _, err := sm.InvokeEndpoint(ctx, driver.InvokeEndpointInput{EndpointName: "ep", Body: []byte("x")}); err == nil {
		t.Error("expected chaos error on InvokeEndpoint")
	}
}

func TestWrapSageMakerNonChaosOpsDelegate(t *testing.T) {
	sm, e := newChaosSageMaker(t)
	ctx := context.Background()

	// A control-plane outage targets "sagemaker"; an unrelated outage must not
	// affect delegated reads.
	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if _, err := sm.CreateModel(ctx, driver.ModelConfig{ModelName: "ok"}); err != nil {
		t.Fatalf("delegated CreateModel should not be affected: %v", err)
	}
}

// TestWrapSageMakerNewMethodsChaosAndDelegation pins that the long-running
// operations added for parity inject chaos, and that the remaining new methods
// pass straight through the wrapper.
func TestWrapSageMakerNewMethodsChaosAndDelegation(t *testing.T) { // T23
	sm, e := newChaosSageMaker(t)
	ctx := context.Background()

	_, _ = sm.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	_, _ = sm.CreateEndpointConfig(ctx, driver.EndpointConfigSpec{
		ConfigName:         "cfg",
		ProductionVariants: []driver.ProductionVariant{{VariantName: "v1", ModelName: "m"}},
	})

	if _, err := sm.CreateEndpoint(ctx, driver.EndpointSpec{EndpointName: "ep", ConfigName: "cfg"}); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	if _, err := sm.CreatePipeline(ctx, driver.PipelineSpec{PipelineName: "pl"}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}

	// A new method with no chaos hook delegates to the inner service.
	if _, err := sm.CreateProject(ctx, driver.ProjectSpec{
		Name: "proj", ServiceCatalog: &driver.ServiceCatalogProvisioning{ProductID: "p", ProvisioningArtifactID: "a"},
	}); err != nil {
		t.Fatalf("delegated CreateProject: %v", err)
	}

	if _, err := sm.DescribeProject(ctx, "proj"); err != nil {
		t.Fatalf("delegated DescribeProject: %v", err)
	}

	e.Apply(chaos.ServiceOutage("sagemaker", time.Hour))

	if _, err := sm.UpdateEndpoint(ctx, driver.UpdateEndpointSpec{EndpointName: "ep", ConfigName: "cfg"}); err == nil {
		t.Error("expected chaos error on UpdateEndpoint")
	}

	if _, err := sm.StartPipelineExecution(ctx, "pl"); err == nil {
		t.Error("expected chaos error on StartPipelineExecution")
	}

	if _, err := sm.CreateHyperParameterTuningJob(ctx, driver.HyperParameterTuningJobConfig{JobName: "hp"}); err == nil {
		t.Error("expected chaos error on CreateHyperParameterTuningJob")
	}
}
