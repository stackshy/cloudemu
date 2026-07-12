package bedrock

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	awsbedrock "github.com/stackshy/cloudemu/v2/providers/aws/bedrock"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
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

func TestManagementWrappers(t *testing.T) {
	b := newTestBedrock()
	ctx := context.Background()

	g, err := b.CreateGuardrail(ctx, driver.GuardrailConfig{
		Name:                    "gr-1",
		BlockedInputMessaging:   "in",
		BlockedOutputsMessaging: "out",
	})
	require.NoError(t, err)
	require.Equal(t, driver.GuardrailReady, g.Status)

	_, err = b.GetGuardrail(ctx, g.ID, "")
	require.NoError(t, err)

	gs, err := b.ListGuardrails(ctx)
	require.NoError(t, err)
	require.Len(t, gs, 1)

	require.NoError(t, b.DeleteGuardrail(ctx, g.ID))

	pt, err := b.CreateProvisionedModelThroughput(ctx, driver.ProvisionedThroughputConfig{
		ProvisionedModelName: "pt-1",
		ModelID:              titanModel,
		ModelUnits:           1,
	})
	require.NoError(t, err)
	require.Equal(t, driver.ProvisionedInService, pt.Status)

	_, err = b.GetProvisionedModelThroughput(ctx, "pt-1")
	require.NoError(t, err)

	pts, err := b.ListProvisionedModelThroughputs(ctx)
	require.NoError(t, err)
	require.Len(t, pts, 1)

	require.NoError(t, b.DeleteProvisionedModelThroughput(ctx, "pt-1"))

	require.NoError(t, b.PutModelInvocationLoggingConfiguration(ctx, driver.LoggingConfig{
		TextDataDeliveryEnabled: true,
		S3:                      &driver.S3LoggingConfig{BucketName: "logs"},
	}))

	lc, err := b.GetModelInvocationLoggingConfiguration(ctx)
	require.NoError(t, err)
	require.NotNil(t, lc)

	require.NoError(t, b.DeleteModelInvocationLoggingConfiguration(ctx))
}
