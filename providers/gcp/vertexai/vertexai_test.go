package vertexai

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
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

func TestLegacyFeaturestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, fs, err := m.CreateFeaturestore(ctx, driver.FeaturestoreConfig{Location: "us-central1", FeaturestoreID: "fs1"})
	require.NoError(t, err)
	assert.Contains(t, fs.Name, "/featurestores/fs1")

	got, err := m.GetFeaturestore(ctx, fs.Name)
	require.NoError(t, err)
	assert.Equal(t, "STABLE", got.State)

	_, et, err := m.CreateEntityType(ctx, fs.Name, "users", "user entities")
	require.NoError(t, err)
	assert.Contains(t, et.Name, "/entityTypes/users")

	ets, err := m.ListEntityTypes(ctx, fs.Name)
	require.NoError(t, err)
	assert.Len(t, ets, 1)

	require.NoError(t, m.WriteFeatureValues(ctx, et.Name, "u1", []driver.FeatureNameValue{{Name: "age", Value: "30"}}))
	vals, err := m.ReadFeatureValues(ctx, et.Name, "u1")
	require.NoError(t, err)
	require.Len(t, vals, 1)
	assert.Equal(t, "age", vals[0].Name)

	_, err = m.ReadFeatureValues(ctx, et.Name, "ghost")
	require.Error(t, err)

	stores, err := m.ListFeaturestores(ctx, "us-central1")
	require.NoError(t, err)
	assert.Len(t, stores, 1)
}

// TestPatchModelDoesNotMutateSnapshot guards the copy-then-Set fix: an earlier
// GetModel snapshot must not change when PatchModel runs.
func TestPatchModelDoesNotMutateSnapshot(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, model, err := m.UploadModel(ctx, driver.ModelConfig{Location: "us-central1", DisplayName: "before"})
	require.NoError(t, err)

	snap, err := m.GetModel(ctx, model.Name)
	require.NoError(t, err)

	_, err = m.PatchModel(ctx, model.Name, "after", "desc")
	require.NoError(t, err)

	assert.Equal(t, "before", snap.DisplayName, "prior snapshot must be unaffected by PatchModel")

	got, err := m.GetModel(ctx, model.Name)
	require.NoError(t, err)
	assert.Equal(t, "after", got.DisplayName)
}

// TestCreateLabelsAreCloned guards against storing the caller's map by
// reference: mutating cfg.Labels after create must not change the stored model.
func TestCreateLabelsAreCloned(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	labels := map[string]string{"team": "ml"}

	_, model, err := m.UploadModel(ctx, driver.ModelConfig{Location: "us-central1", DisplayName: "m", Labels: labels})
	require.NoError(t, err)

	labels["team"] = "mutated"

	got, err := m.GetModel(ctx, model.Name)
	require.NoError(t, err)
	assert.Equal(t, "ml", got.Labels["team"], "stored labels must be a clone of the caller's map")
}

// TestDeployModelKeepsTrafficForAllModels guards the traffic-split merge fix:
// deploying a second model must not drop the first from the split.
func TestDeployModelKeepsTrafficForAllModels(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, ep, err := m.CreateEndpoint(ctx, driver.EndpointConfig{Location: "us-central1", DisplayName: "ep"})
	require.NoError(t, err)

	_, _, err = m.DeployModel(ctx, ep.Name, driver.DeployedModel{ID: "A", Model: "m/A"})
	require.NoError(t, err)

	_, after, err := m.DeployModel(ctx, ep.Name, driver.DeployedModel{ID: "B", Model: "m/B"})
	require.NoError(t, err)

	require.Len(t, after.DeployedModels, 2)
	assert.Contains(t, after.TrafficSplit, "A", "A must keep a traffic-split entry after B deploys")
	assert.Contains(t, after.TrafficSplit, "B")

	total := 0
	for _, v := range after.TrafficSplit {
		total += v
	}

	assert.Equal(t, 100, total, "traffic split must total 100")

	// Undeploy rebalances onto the survivor.
	_, post, err := m.UndeployModel(ctx, ep.Name, "A")
	require.NoError(t, err)
	assert.NotContains(t, post.TrafficSplit, "A")
	assert.Equal(t, 100, post.TrafficSplit["B"])
}

// TestCachedContentTTLAndContents guards the expiry/persist fix.
func TestCachedContentTTLAndContents(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cc, err := m.CreateCachedContent(ctx, driver.CachedContentConfig{
		Location: "us-central1", Model: "gemini-2.5-pro", TTLSeconds: 3600,
		Contents: []driver.Content{{Role: "user", Parts: []driver.Part{{Text: "hi"}}}},
	})
	require.NoError(t, err)

	assert.NotEqual(t, cc.CreateTime, cc.ExpireTime, "expireTime must be createTime + TTL, not equal")
	assert.Greater(t, cc.ExpireTime, cc.CreateTime)
	require.Len(t, cc.Contents, 1, "contents must be persisted")
	assert.Equal(t, "hi", cc.Contents[0].Parts[0].Text)
}

// TestDeleteModelVersionActuallyDeletes guards the no-op fix.
func TestDeleteModelVersionActuallyDeletes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, model, err := m.UploadModel(ctx, driver.ModelConfig{Location: "us-central1", DisplayName: "m"})
	require.NoError(t, err)

	_, err = m.DeleteModelVersion(ctx, model.Name+"@1")
	require.NoError(t, err)

	_, err = m.GetModel(ctx, model.Name)
	require.Error(t, err, "model must be gone after version delete")

	_, err = m.DeleteModelVersion(ctx, model.Name+"@1")
	require.Error(t, err, "deleting a missing version must return NotFound")
}

// TestChildListingPrefixDelimiter guards against sibling leakage between
// featureGroups whose IDs share a prefix (fg1 vs fg10).
func TestChildListingPrefixDelimiter(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, fg1, err := m.CreateFeatureGroup(ctx, driver.FeatureGroupConfig{Location: "us-central1", FeatureGroupID: "fg1"})
	require.NoError(t, err)
	_, fg10, err := m.CreateFeatureGroup(ctx, driver.FeatureGroupConfig{Location: "us-central1", FeatureGroupID: "fg10"})
	require.NoError(t, err)

	_, _, err = m.CreateFeature(ctx, fg1.Name, "f1", "")
	require.NoError(t, err)
	_, _, err = m.CreateFeature(ctx, fg10.Name, "f10a", "")
	require.NoError(t, err)
	_, _, err = m.CreateFeature(ctx, fg10.Name, "f10b", "")
	require.NoError(t, err)

	f1, err := m.ListFeatures(ctx, fg1.Name)
	require.NoError(t, err)
	assert.Len(t, f1, 1, "fg1 listing must not leak fg10's features")

	f10, err := m.ListFeatures(ctx, fg10.Name)
	require.NoError(t, err)
	assert.Len(t, f10, 2)
}
