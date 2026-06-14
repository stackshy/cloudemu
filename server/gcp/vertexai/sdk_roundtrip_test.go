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

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
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
