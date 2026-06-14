package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/sagemaker/driver"
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
