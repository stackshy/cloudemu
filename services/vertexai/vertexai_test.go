package vertexai_test

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
	gcpvertex "github.com/stackshy/cloudemu/v2/providers/gcp/vertexai"
	"github.com/stackshy/cloudemu/v2/services/vertexai"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func newPortable(opts ...vertexai.Option) *vertexai.VertexAI {
	o := config.NewOptions(config.WithProjectID("test-project"))

	return vertexai.New(gcpvertex.New(o), opts...)
}

func TestPortableRecordsAndCountsCalls(t *testing.T) {
	rec := recorder.New()
	col := metrics.NewCollector()
	v := newPortable(vertexai.WithRecorder(rec), vertexai.WithMetrics(col))
	ctx := context.Background()

	_, _, err := v.CreateDataset(ctx, driver.DatasetConfig{Location: "us-central1", DisplayName: "ds1"})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCountFor("vertexai", "CreateDataset"))
	assert.NotEmpty(t, col.All())
}

func TestPortableErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	boom := errors.New("injected")
	inj.Set("vertexai", "UploadModel", boom, inject.Always{})

	v := newPortable(vertexai.WithErrorInjection(inj))

	_, _, err := v.UploadModel(context.Background(), driver.ModelConfig{Location: "us-central1", DisplayName: "m"})
	require.ErrorIs(t, err, boom)
}

func TestPortableLatencyApplied(t *testing.T) {
	v := newPortable(vertexai.WithLatency(20 * time.Millisecond))

	start := time.Now()
	_, err := v.ListModels(context.Background(), "us-central1")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

func TestPortableForwardsResults(t *testing.T) {
	v := newPortable()
	ctx := context.Background()

	_, m, err := v.UploadModel(ctx, driver.ModelConfig{Location: "us-central1", DisplayName: "m"})
	require.NoError(t, err)

	got, err := v.GetModel(ctx, m.Name)
	require.NoError(t, err)
	assert.Equal(t, "m", got.DisplayName)
}
