package sagemaker_test

import (
	"context"
	"errors"
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
	_, err := s.ListModels(context.Background())
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
