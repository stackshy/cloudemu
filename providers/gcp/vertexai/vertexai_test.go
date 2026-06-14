package vertexai

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/vertexai/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithProjectID("proj"))

	return New(opts)
}

func TestModelUploadAndGet(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	op, model, err := m.UploadModel(ctx, driver.ModelConfig{Location: "us-central1", DisplayName: "m1"})
	require.NoError(t, err)
	assert.True(t, op.Done)
	assert.Contains(t, model.Name, "projects/proj/locations/us-central1/models/")

	got, err := m.GetModel(ctx, model.Name)
	require.NoError(t, err)
	assert.Equal(t, "m1", got.DisplayName)

	// version-suffixed name resolves to the base model.
	got2, err := m.GetModel(ctx, model.Name+"@1")
	require.NoError(t, err)
	assert.Equal(t, model.Name, got2.Name)
}

func TestEndpointDeployAndPredict(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, ep, err := m.CreateEndpoint(ctx, driver.EndpointConfig{Location: "us-central1", DisplayName: "ep"})
	require.NoError(t, err)

	_, _, err = m.DeployModel(ctx, ep.Name, driver.DeployedModel{Model: "projects/proj/locations/us-central1/models/m", DisplayName: "v1"})
	require.NoError(t, err)

	got, err := m.GetEndpoint(ctx, ep.Name)
	require.NoError(t, err)
	require.Len(t, got.DeployedModels, 1)

	resp, err := m.Predict(ctx, driver.PredictRequest{Endpoint: ep.Name, Instances: []any{map[string]any{"x": 1}}})
	require.NoError(t, err)
	assert.Len(t, resp.Predictions, 1)
	assert.NotEmpty(t, resp.DeployedModelID)
}

func TestGenerateContent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	resp, err := m.GenerateContent(ctx, "publishers/google/models/gemini-2.5-pro", driver.GenerateContentRequest{
		Contents: []driver.Content{{Role: "user", Parts: []driver.Part{{Text: "hello there world"}}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "model", resp.Candidates[0].Content.Role)
	assert.Equal(t, 3, resp.UsageMetadata.PromptTokenCount)
	assert.Positive(t, resp.UsageMetadata.TotalTokenCount)
}

func TestJobsSynchronousSuccess(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cj, err := m.CreateCustomJob(ctx, driver.CustomJobConfig{Location: "us-central1", DisplayName: "train"})
	require.NoError(t, err)
	assert.Equal(t, driver.JobStateSucceeded, cj.State)

	bp, err := m.CreateBatchPredictionJob(ctx, driver.BatchPredictionJobConfig{Location: "us-central1", DisplayName: "bp"})
	require.NoError(t, err)
	assert.Equal(t, driver.JobStateSucceeded, bp.State)

	require.NoError(t, m.CancelCustomJob(ctx, cj.Name))
	got, _ := m.GetCustomJob(ctx, cj.Name)
	assert.Equal(t, driver.JobStateCancelled, got.State)
}

func TestVectorSearchFindNeighbors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, idx, err := m.CreateIndex(ctx, driver.IndexConfig{Location: "us-central1", DisplayName: "idx", Dimensions: 4})
	require.NoError(t, err)
	require.NoError(t, m.UpsertDatapoints(ctx, idx.Name, []driver.Datapoint{{DatapointID: "a", FeatureVector: []float64{1, 2, 3, 4}}}))

	got, err := m.GetIndex(ctx, idx.Name)
	require.NoError(t, err)
	assert.Equal(t, 1, got.DatapointCount)

	_, ie, err := m.CreateIndexEndpoint(ctx, driver.IndexEndpointConfig{Location: "us-central1", DisplayName: "ie"})
	require.NoError(t, err)
	_, _, err = m.DeployIndex(ctx, ie.Name, driver.DeployedIndex{ID: "d1", Index: idx.Name})
	require.NoError(t, err)

	nbrs, err := m.FindNeighbors(ctx, ie.Name, "d1", []float64{1, 2, 3, 4}, 3)
	require.NoError(t, err)
	assert.Len(t, nbrs, 3)
}

func TestOperationsRetrievable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	op, _, err := m.CreateEndpoint(ctx, driver.EndpointConfig{Location: "us-central1", DisplayName: "ep"})
	require.NoError(t, err)

	got, err := m.GetOperation(ctx, op.Name)
	require.NoError(t, err)
	assert.True(t, got.Done)
}

func TestNotFound(t *testing.T) {
	m := newTestMock()
	_, err := m.GetModel(context.Background(), "projects/proj/locations/us-central1/models/ghost")
	require.Error(t, err)
}
