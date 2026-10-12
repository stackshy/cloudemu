package sagemaker_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	awssm "github.com/stackshy/cloudemu/v2/providers/aws/sagemaker"
	"github.com/stackshy/cloudemu/v2/services/sagemaker"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

func newPortable(opts ...sagemaker.Option) *sagemaker.SageMaker {
	o := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))

	return sagemaker.New(awssm.New(o), opts...)
}

func TestPortableRecordsAndCountsCalls(t *testing.T) {
	rec := recorder.New()
	col := metrics.NewCollector()
	s := newPortable(sagemaker.WithRecorder(rec), sagemaker.WithMetrics(col))
	ctx := context.Background()

	_, err := s.CreateTrainingJob(ctx, driver.TrainingJobConfig{JobName: "j1"})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCountFor("sagemaker", "CreateTrainingJob"))
	assert.NotEmpty(t, col.All())
}

func TestPortableErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	boom := errors.New("injected")
	inj.Set("sagemaker", "CreateModel", boom, inject.Always{})

	s := newPortable(sagemaker.WithErrorInjection(inj))

	_, err := s.CreateModel(context.Background(), driver.ModelConfig{ModelName: "m"})
	require.ErrorIs(t, err, boom)
}

func TestPortableLatencyApplied(t *testing.T) {
	s := newPortable(sagemaker.WithLatency(20 * time.Millisecond))

	start := time.Now()
	_, err := s.ListModels(context.Background(), driver.ListOptions{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

func TestPortableForwardsResults(t *testing.T) {
	s := newPortable()
	ctx := context.Background()

	_, err := s.CreateModel(ctx, driver.ModelConfig{ModelName: "m"})
	require.NoError(t, err)

	got, err := s.DescribeModel(ctx, "m")
	require.NoError(t, err)
	assert.Equal(t, "m", got.ModelName)
}

// TestPortableEveryOperationRoutesThroughThePipeline injects an error for each
// operation name and calls the matching wrapper method: the injected error must
// come back, proving every method applies the cross-cutting pipeline under its
// own operation name.
func TestPortableEveryOperationRoutesThroughThePipeline(t *testing.T) {
	boom := errors.New("injected")
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errType := reflect.TypeOf((*error)(nil)).Elem()

	typ := reflect.TypeOf((*sagemaker.SageMaker)(nil))
	checked := 0

	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		mt := method.Type

		if mt.NumIn() < 2 || !mt.In(1).Implements(ctxType) || mt.NumOut() == 0 || !mt.Out(mt.NumOut()-1).Implements(errType) {
			continue
		}

		inj := inject.NewInjector()
		inj.Set("sagemaker", method.Name, boom, inject.Always{})

		s := newPortable(sagemaker.WithErrorInjection(inj), sagemaker.WithRecorder(recorder.New()))

		args := []reflect.Value{reflect.ValueOf(s), reflect.ValueOf(context.Background())}
		for a := 2; a < mt.NumIn(); a++ {
			args = append(args, reflect.Zero(mt.In(a)))
		}

		out := method.Func.Call(args)
		got, _ := out[len(out)-1].Interface().(error)
		assert.ErrorIs(t, got, boom, "%s must route through the pipeline under its own name", method.Name)

		checked++
	}

	assert.Greater(t, checked, 190, "every driver operation is covered")
}

// TestPortableEveryOperationDelegatesToTheDriver calls every wrapper method with
// zero inputs against the in-memory driver and requires each call to be
// recorded under its own name, so the delegation of every operation runs.
func TestPortableEveryOperationDelegatesToTheDriver(t *testing.T) {
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errType := reflect.TypeOf((*error)(nil)).Elem()
	typ := reflect.TypeOf((*sagemaker.SageMaker)(nil))
	checked := 0

	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		mt := method.Type

		if mt.NumIn() < 2 || !mt.In(1).Implements(ctxType) || mt.NumOut() == 0 || !mt.Out(mt.NumOut()-1).Implements(errType) {
			continue
		}

		rec := recorder.New()
		s := newPortable(sagemaker.WithRecorder(rec))

		args := []reflect.Value{reflect.ValueOf(s), reflect.ValueOf(context.Background())}
		for a := 2; a < mt.NumIn(); a++ {
			args = append(args, reflect.Zero(mt.In(a)))
		}

		assert.NotPanics(t, func() { method.Func.Call(args) }, method.Name)
		assert.Equal(t, 1, rec.CallCountFor("sagemaker", method.Name), "%s is recorded once under its own name", method.Name)

		checked++
	}

	assert.Greater(t, checked, 190)
}
