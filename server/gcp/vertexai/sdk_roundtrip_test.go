package vertexai_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const base = "/v1/projects/mock-project/locations/us-central1"

func newServer(t *testing.T) string {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{
		VertexAI: cloud.VertexAI,
		// Firestore included to exercise routing precedence: Vertex must claim
		// its collections before Firestore's permissive /v1/projects/ prefix.
		Firestore: cloud.Firestore,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts.URL
}

func do(t *testing.T, method, url string, body any) map[string]any {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "method=%s url=%s body=%s", method, url, raw)

	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out), "body=%s", raw)
	}

	return out
}

func TestModelUploadAndGet(t *testing.T) {
	url := newServer(t)

	op := do(t, http.MethodPost, url+base+"/models:upload", map[string]any{
		"model": map[string]any{"displayName": "m1", "containerSpec": map[string]any{"imageUri": "img:latest"}},
	})
	assert.Equal(t, true, op["done"])
	name, _ := op["response"].(map[string]any)["name"].(string)
	require.NotEmpty(t, name)

	// The done LRO response must carry the typed Model (with @type and
	// versionId), not just {name}, so SDK pollers can read the result.
	resp := op["response"].(map[string]any)
	assert.Contains(t, resp["@type"], "google.cloud.aiplatform.v1.Model")
	assert.Equal(t, "1", resp["versionId"])
	assert.Equal(t, "m1", resp["displayName"])

	got := do(t, http.MethodGet, url+"/v1/"+name, nil)
	assert.Equal(t, "m1", got["displayName"])

	list := do(t, http.MethodGet, url+base+"/models", nil)
	assert.Len(t, list["models"], 1)
}

func TestEndpointDeployPredict(t *testing.T) {
	url := newServer(t)

	op := do(t, http.MethodPost, url+base+"/endpoints", map[string]any{"displayName": "ep"})
	assert.Equal(t, true, op["done"])
	epName := op["response"].(map[string]any)["name"].(string)
	require.NotEmpty(t, epName)

	do(t, http.MethodPost, url+"/v1/"+epName+":deployModel", map[string]any{
		"deployedModel": map[string]any{"model": base + "/models/m", "displayName": "v1"},
	})

	got := do(t, http.MethodGet, url+"/v1/"+epName, nil)
	assert.Len(t, got["deployedModels"], 1)

	pred := do(t, http.MethodPost, url+"/v1/"+epName+":predict", map[string]any{
		"instances": []any{map[string]any{"x": 1}},
	})
	assert.Len(t, pred["predictions"], 1)
	assert.NotEmpty(t, pred["deployedModelId"])
}

func TestPublishersGenerateContent(t *testing.T) {
	url := newServer(t)

	resp := do(t, http.MethodPost, url+"/v1/publishers/google/models/gemini-2.5-pro:generateContent", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello there world"}}}},
	})

	cands, ok := resp["candidates"].([]any)
	require.True(t, ok)
	require.Len(t, cands, 1)
	usage := resp["usageMetadata"].(map[string]any)
	assert.EqualValues(t, 3, usage["promptTokenCount"])

	ct := do(t, http.MethodPost, url+"/v1/publishers/google/models/gemini-2.5-pro:countTokens", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "one two"}}}},
	})
	assert.EqualValues(t, 2, ct["totalTokens"])
}

// TestStreamGenerateContentReturnsArray verifies :streamGenerateContent emits a
// JSON array of chunks (not a lone object), which SDK stream decoders require.
func TestStreamGenerateContentReturnsArray(t *testing.T) {
	url := newServer(t)

	body, _ := json.Marshal(map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello there world"}}}},
	})

	req, err := http.NewRequest(http.MethodPost,
		url+"/v1/publishers/google/models/gemini-2.5-pro:streamGenerateContent", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var chunks []map[string]any
	require.NoError(t, json.Unmarshal(raw, &chunks), "stream response must be a JSON array, got %s", raw)
	require.GreaterOrEqual(t, len(chunks), 1)
	assert.Contains(t, chunks[0], "candidates")
}

func TestCustomJobSynchronous(t *testing.T) {
	url := newServer(t)

	job := do(t, http.MethodPost, url+base+"/customJobs", map[string]any{"displayName": "train"})
	assert.Equal(t, "JOB_STATE_SUCCEEDED", job["state"])
	name := job["name"].(string)

	got := do(t, http.MethodGet, url+"/v1/"+name, nil)
	assert.Equal(t, "JOB_STATE_SUCCEEDED", got["state"])

	do(t, http.MethodPost, url+"/v1/"+name+":cancel", map[string]any{})
}

func TestDatasetCreate(t *testing.T) {
	url := newServer(t)

	op := do(t, http.MethodPost, url+base+"/datasets", map[string]any{"displayName": "ds"})
	assert.Equal(t, true, op["done"])

	list := do(t, http.MethodGet, url+base+"/datasets", nil)
	assert.Len(t, list["datasets"], 1)
}

func opName(t *testing.T, op map[string]any) string {
	t.Helper()

	assert.Equal(t, true, op["done"])
	resp, ok := op["response"].(map[string]any)
	require.True(t, ok, "operation missing response: %v", op)
	name, _ := resp["name"].(string)
	require.NotEmpty(t, name)

	return name
}

func TestTrainingPipelineAndPipelineJob(t *testing.T) {
	url := newServer(t)

	tp := do(t, http.MethodPost, url+base+"/trainingPipelines", map[string]any{"displayName": "tp"})
	assert.Equal(t, "PIPELINE_STATE_SUCCEEDED", tp["state"])
	do(t, http.MethodGet, url+"/v1/"+tp["name"].(string), nil)
	assert.Len(t, do(t, http.MethodGet, url+base+"/trainingPipelines", nil)["trainingPipelines"], 1)
	do(t, http.MethodPost, url+"/v1/"+tp["name"].(string)+":cancel", map[string]any{})

	pj := do(t, http.MethodPost, url+base+"/pipelineJobs", map[string]any{"displayName": "pj"})
	assert.Equal(t, "PIPELINE_STATE_SUCCEEDED", pj["state"])
	assert.Len(t, do(t, http.MethodGet, url+base+"/pipelineJobs", nil)["pipelineJobs"], 1)
}

func TestTuningAndHyperparameterJobs(t *testing.T) {
	url := newServer(t)

	tj := do(t, http.MethodPost, url+base+"/tuningJobs", map[string]any{"baseModel": "gemini-2.5-pro"})
	require.NotEmpty(t, tj["name"])
	assert.Len(t, do(t, http.MethodGet, url+base+"/tuningJobs", nil)["tuningJobs"], 1)

	hpo := do(t, http.MethodPost, url+base+"/hyperparameterTuningJobs", map[string]any{
		"displayName": "hpo", "maxTrialCount": 4,
	})
	require.NotEmpty(t, hpo["name"])
	assert.EqualValues(t, 4, hpo["maxTrialCount"])
	do(t, http.MethodPost, url+"/v1/"+hpo["name"].(string)+":cancel", map[string]any{})
}

func TestCachedContents(t *testing.T) {
	url := newServer(t)

	cc := do(t, http.MethodPost, url+base+"/cachedContents", map[string]any{
		"model": "gemini-2.5-pro", "displayName": "ctx",
	})
	require.NotEmpty(t, cc["name"])
	assert.Len(t, do(t, http.MethodGet, url+base+"/cachedContents", nil)["cachedContents"], 1)
	do(t, http.MethodDelete, url+"/v1/"+cc["name"].(string), nil)
}

func TestFeaturestoreAndEntityTypes(t *testing.T) {
	url := newServer(t)

	fsName := opName(t, do(t, http.MethodPost, url+base+"/featurestores?featurestoreId=fs1", map[string]any{}))
	etName := opName(t, do(t, http.MethodPost, url+"/v1/"+fsName+"/entityTypes?entityTypeId=user", map[string]any{
		"entityType": map[string]any{"description": "users"},
	}))

	do(t, http.MethodPost, url+"/v1/"+etName+":writeFeatureValues", map[string]any{
		"payloads": []any{map[string]any{"entityId": "u1", "featureValues": map[string]any{"age": "30"}}},
	})
	read := do(t, http.MethodPost, url+"/v1/"+etName+":readFeatureValues", map[string]any{"entityId": "u1"})
	require.NotNil(t, read["entityView"])

	assert.Len(t, do(t, http.MethodGet, url+"/v1/"+fsName+"/entityTypes", nil)["entityTypes"], 1)
}

func TestFeatureRegistryAndOnlineStore(t *testing.T) {
	url := newServer(t)

	fgName := opName(t, do(t, http.MethodPost, url+base+"/featureGroups?featureGroupId=fg1", map[string]any{
		"bigQuery": map[string]any{"bigQuerySource": map[string]any{"inputUri": "bq://t"}},
	}))
	opName(t, do(t, http.MethodPost, url+"/v1/"+fgName+"/features?featureId=f1", map[string]any{"description": "feat"}))
	assert.Len(t, do(t, http.MethodGet, url+"/v1/"+fgName+"/features", nil)["features"], 1)

	osName := opName(t, do(t, http.MethodPost, url+base+"/featureOnlineStores?featureOnlineStoreId=os1", map[string]any{}))
	fvName := opName(t, do(t, http.MethodPost, url+"/v1/"+osName+"/featureViews?featureViewId=fv1", map[string]any{
		"bigQuerySource": map[string]any{"uri": "bq://t"},
	}))
	do(t, http.MethodPost, url+"/v1/"+fvName+":fetchFeatureValues", map[string]any{
		"dataKey": map[string]any{"key": "u1"},
	})
}

func TestIndexesAndVectorSearch(t *testing.T) {
	url := newServer(t)

	idxName := opName(t, do(t, http.MethodPost, url+base+"/indexes", map[string]any{
		"displayName": "idx", "metadata": map[string]any{"config": map[string]any{"dimensions": 8}},
	}))
	do(t, http.MethodPost, url+"/v1/"+idxName+":upsertDatapoints", map[string]any{
		"datapoints": []any{map[string]any{"datapointId": "d1", "featureVector": []any{0.1, 0.2}}},
	})
	got := do(t, http.MethodGet, url+"/v1/"+idxName, nil)
	assert.EqualValues(t, 1, got["indexStats"].(map[string]any)["vectorsCount"])

	ieName := opName(t, do(t, http.MethodPost, url+base+"/indexEndpoints", map[string]any{"displayName": "ie"}))
	do(t, http.MethodPost, url+"/v1/"+ieName+":deployIndex", map[string]any{
		"deployedIndex": map[string]any{"id": "di1", "index": idxName},
	})
	nn := do(t, http.MethodPost, url+"/v1/"+ieName+":findNeighbors", map[string]any{
		"deployedIndexId": "di1",
		"queries":         []any{map[string]any{"datapoint": map[string]any{"featureVector": []any{0.1}}, "neighborCount": 3}},
	})
	require.NotNil(t, nn["nearestNeighbors"])
}

func TestMetadataTensorboardScheduleNotebook(t *testing.T) {
	url := newServer(t)

	opName(t, do(t, http.MethodPost, url+base+"/metadataStores?metadataStoreId=ms1", map[string]any{}))
	assert.Len(t, do(t, http.MethodGet, url+base+"/metadataStores", nil)["metadataStores"], 1)

	opName(t, do(t, http.MethodPost, url+base+"/tensorboards", map[string]any{"displayName": "tb"}))

	sched := do(t, http.MethodPost, url+base+"/schedules", map[string]any{"displayName": "sc", "cron": "0 * * * *"})
	assert.Equal(t, "ACTIVE", sched["state"])
	do(t, http.MethodPost, url+"/v1/"+sched["name"].(string)+":pause", map[string]any{})
	assert.Equal(t, "PAUSED", do(t, http.MethodGet, url+"/v1/"+sched["name"].(string), nil)["state"])
	do(t, http.MethodPost, url+"/v1/"+sched["name"].(string)+":resume", map[string]any{})

	opName(t, do(t, http.MethodPost, url+base+"/notebookRuntimeTemplates", map[string]any{
		"displayName": "nt", "machineSpec": map[string]any{"machineType": "n1-standard-4"},
	}))
	nr := opName(t, do(t, http.MethodPost, url+base+"/notebookRuntimes:assign", map[string]any{
		"notebookRuntime": map[string]any{"displayName": "nr"},
	}))
	do(t, http.MethodPost, url+"/v1/"+nr+":stop", map[string]any{})
	assert.Equal(t, "STOPPED", do(t, http.MethodGet, url+"/v1/"+nr, nil)["runtimeState"])
	do(t, http.MethodPost, url+"/v1/"+nr+":start", map[string]any{})
}

func TestModelVersionsAndEvaluations(t *testing.T) {
	url := newServer(t)

	mName := opName(t, do(t, http.MethodPost, url+base+"/models:upload", map[string]any{
		"model": map[string]any{"displayName": "m1"},
	}))

	versions := do(t, http.MethodGet, url+"/v1/"+mName+":listVersions", nil)
	assert.Len(t, versions["models"], 1)

	ev := do(t, http.MethodPost, url+"/v1/"+mName+"/evaluations", map[string]any{
		"displayName": "eval1", "metricsSchemaUri": "schema://x",
	})
	require.NotEmpty(t, ev["name"])
	assert.Len(t, do(t, http.MethodGet, url+"/v1/"+mName+"/evaluations", nil)["modelEvaluations"], 1)
}

func TestNotFoundMapsTo404(t *testing.T) {
	url := newServer(t)

	req, _ := http.NewRequest(http.MethodGet, url+base+"/models/ghost", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.True(t, strings.Contains(string(body), "notFound"))
}
