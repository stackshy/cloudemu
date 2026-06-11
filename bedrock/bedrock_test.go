package bedrock

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/bedrock/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	awsbedrock "github.com/stackshy/cloudemu/providers/aws/bedrock"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const titanModel = "amazon.titan-text-express-v1"

func newTestBedrock(opts ...Option) *Bedrock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewBedrock(awsbedrock.New(o), opts...)
}

func TestListAndGetFoundationModels(t *testing.T) {
	b := newTestBedrock()
	ctx := context.Background()

	models, err := b.ListFoundationModels(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, models)

	fm, err := b.GetFoundationModel(ctx, titanModel)
	require.NoError(t, err)
	assert.Equal(t, titanModel, fm.ModelID)
}

func TestCustomizationLifecycle(t *testing.T) {
	b := newTestBedrock()
	ctx := context.Background()

	job, err := b.CreateModelCustomizationJob(ctx, driver.CustomizationJobConfig{
		JobName:             "job-1",
		CustomModelName:     "cm-1",
		RoleARN:             "arn:aws:iam::123456789012:role/bedrock",
		BaseModelIdentifier: titanModel,
	})
	require.NoError(t, err)
	assert.Equal(t, driver.JobCompleted, job.Status)

	got, err := b.GetModelCustomizationJob(ctx, "job-1")
	require.NoError(t, err)
	assert.Equal(t, "job-1", got.JobName)

	jobs, err := b.ListModelCustomizationJobs(ctx)
	require.NoError(t, err)
	assert.Len(t, jobs, 1)

	models, err := b.ListCustomModels(ctx)
	require.NoError(t, err)
	assert.Len(t, models, 1)

	cm, err := b.GetCustomModel(ctx, "cm-1")
	require.NoError(t, err)
	assert.Equal(t, driver.ModelActive, cm.ModelStatus)

	require.NoError(t, b.DeleteCustomModel(ctx, "cm-1"))

	_, err = b.GetCustomModel(ctx, "cm-1")
	require.Error(t, err)
}

func TestInvokeAndConverse(t *testing.T) {
	b := newTestBedrock()
	ctx := context.Background()

	res, err := b.InvokeModel(ctx, driver.InvokeModelInput{
		ModelID:     titanModel,
		ContentType: "application/json",
		Body:        []byte(`{"inputText":"hello"}`),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Body)

	out, err := b.Converse(ctx, driver.ConverseInput{
		ModelID:  titanModel,
		Messages: []driver.Message{{Role: "user", Text: []string{"hello"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "end_turn", out.StopReason)
	assert.NotZero(t, out.TotalTokens)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	b := newTestBedrock(WithRecorder(rec))

	_, err := b.ListFoundationModels(context.Background())
	require.NoError(t, err)

	calls := rec.Calls()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.Equal(t, "bedrock", calls[0].Service)
	assert.Equal(t, "ListFoundationModels", calls[0].Operation)
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	b := newTestBedrock(WithMetrics(mc))

	_, err := b.ListFoundationModels(context.Background())
	require.NoError(t, err)

	q := metrics.NewQuery(mc)
	assert.GreaterOrEqual(t, q.ByName("calls_total").Count(), 1)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	b := newTestBedrock(WithErrorInjection(inj))

	inj.Set("bedrock", "ListFoundationModels", fmt.Errorf("injected failure"), inject.Always{})

	_, err := b.ListFoundationModels(context.Background())
	require.Error(t, err)
}

func TestWithLatency(t *testing.T) {
	b := newTestBedrock(WithLatency(time.Millisecond))

	start := time.Now()
	_, err := b.ListFoundationModels(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), time.Millisecond)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	lim := ratelimit.New(1, 1, fc)
	b := NewBedrock(awsbedrock.New(o), WithRateLimiter(lim))

	_, err := b.ListFoundationModels(context.Background())
	require.NoError(t, err)

	_, err = b.ListFoundationModels(context.Background())
	require.Error(t, err)
}
