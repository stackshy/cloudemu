package streamanalytics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/streamanalytics"
	streamanalyticssrv "github.com/stackshy/cloudemu/v2/server/azure/streamanalytics"
)

const (
	apiVer  = "?api-version=2020-03-01"
	jobBase = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.StreamAnalytics/streamingjobs/"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := streamanalytics.New(config.NewOptions())
	srv := httptest.NewServer(streamanalyticssrv.New(mock))
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

const jobBody = `{
	"location": "West US",
	"tags": {"env": "dev"},
	"properties": {
		"sku": {"name": "Standard"},
		"eventsOutOfOrderPolicy": "Drop",
		"outputErrorPolicy": "Drop",
		"eventsOutOfOrderMaxDelayInSeconds": 0,
		"eventsLateArrivalMaxDelayInSeconds": 5,
		"compatibilityLevel": "1.0"
	}
}`

func createJob(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()

	code, raw := do(t, srv, http.MethodPut, jobBase+"job1"+apiVer, jobBody)
	if code != http.StatusCreated {
		t.Fatalf("create job status = %d, body=%s", code, raw)
	}

	return decode(t, raw)
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}

	return m
}

func props(m map[string]any) map[string]any {
	p, _ := m["properties"].(map[string]any)

	return p
}

func TestCreateGetJobByteStable(t *testing.T) {
	srv := newServer(t)
	createJob(t, srv)

	code1, raw1 := do(t, srv, http.MethodGet, jobBase+"job1"+apiVer, "")
	if code1 != http.StatusOK {
		t.Fatalf("get status = %d", code1)
	}

	_, raw2 := do(t, srv, http.MethodGet, jobBase+"job1"+apiVer, "")

	if !bytes.Equal(raw1, raw2) {
		t.Fatalf("two GETs not byte-identical:\n%s\n%s", raw1, raw2)
	}

	p := props(decode(t, raw1))
	if p["provisioningState"] != "Succeeded" || p["jobState"] != "Created" {
		t.Errorf("state fields wrong: %v", p)
	}

	if p["jobId"] == "" || p["etag"] == "" {
		t.Errorf("jobId/etag missing: %v", p)
	}
}

func TestJobIDStableAcrossPatch(t *testing.T) {
	srv := newServer(t)
	created := createJob(t, srv)
	jobID := props(created)["jobId"]
	etag := props(created)["etag"]

	code, raw := do(t, srv, http.MethodPatch, jobBase+"job1"+apiVer, `{"tags":{"env":"prod"}}`)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d, %s", code, raw)
	}

	patched := decode(t, raw)
	if props(patched)["jobId"] != jobID || props(patched)["etag"] != etag {
		t.Errorf("jobId/etag drifted on patch")
	}

	tags, _ := patched["tags"].(map[string]any)
	if tags["env"] != "prod" {
		t.Errorf("tags not replaced: %v", tags)
	}
}

func TestStartStopViaWire(t *testing.T) {
	srv := newServer(t)
	createJob(t, srv)

	code, raw := do(t, srv, http.MethodPost, jobBase+"job1/start"+apiVer, `{"outputStartMode":"JobStartTime"}`)
	if code != http.StatusOK {
		t.Fatalf("start status = %d, %s", code, raw)
	}

	if props(decode(t, raw))["jobState"] != "Running" {
		t.Fatalf("jobState after start = %v", props(decode(t, raw))["jobState"])
	}

	// Start again → 409.
	code, _ = do(t, srv, http.MethodPost, jobBase+"job1/start"+apiVer, `{"outputStartMode":"JobStartTime"}`)
	if code != http.StatusConflict {
		t.Errorf("double start status = %d, want 409", code)
	}

	code, raw = do(t, srv, http.MethodPost, jobBase+"job1/stop"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("stop status = %d, %s", code, raw)
	}

	if props(decode(t, raw))["jobState"] != "Stopped" {
		t.Errorf("jobState after stop = %v", props(decode(t, raw))["jobState"])
	}

	// Stop again → 409.
	code, _ = do(t, srv, http.MethodPost, jobBase+"job1/stop"+apiVer, "")
	if code != http.StatusConflict {
		t.Errorf("double stop status = %d, want 409", code)
	}
}

func TestChildLifecycleAndEmbedding(t *testing.T) {
	srv := newServer(t)
	createJob(t, srv)

	transformation := `{"properties":{"streamingUnits":3,"query":"SELECT * INTO o FROM i"}}`
	code, _ := do(t, srv, http.MethodPut, jobBase+"job1/transformations/main"+apiVer, transformation)
	if code != http.StatusCreated {
		t.Fatalf("create transformation = %d", code)
	}

	input := `{"properties":{"type":"Stream","datasource":{"type":"Microsoft.ServiceBus/EventHub",` +
		`"properties":{"eventHubName":"eh"}},"serialization":{"type":"Json"}}}`
	code, raw := do(t, srv, http.MethodPut, jobBase+"job1/inputs/in1"+apiVer, input)
	if code != http.StatusCreated {
		t.Fatalf("create input = %d, %s", code, raw)
	}

	inProps := props(decode(t, raw))
	if inProps["etag"] == "" {
		t.Errorf("input etag missing")
	}

	ds, _ := inProps["datasource"].(map[string]any)
	if ds["type"] != "Microsoft.ServiceBus/EventHub" {
		t.Errorf("datasource discriminator lost: %v", inProps["datasource"])
	}

	output := `{"properties":{"datasource":{"type":"Microsoft.Storage/Blob","properties":{"container":"c"}}}}`
	if code, _ = do(t, srv, http.MethodPut, jobBase+"job1/outputs/out1"+apiVer, output); code != http.StatusCreated {
		t.Fatalf("create output = %d", code)
	}

	// GET job with $expand → embedded transformation + inputs + outputs.
	_, raw = do(t, srv, http.MethodGet, jobBase+"job1"+apiVer+"&$expand=transformation,inputs,outputs", "")
	p := props(decode(t, raw))

	if p["transformation"] == nil {
		t.Errorf("transformation not embedded")
	}

	inputs, _ := p["inputs"].([]any)
	outputs, _ := p["outputs"].([]any)
	if len(inputs) != 1 || len(outputs) != 1 {
		t.Errorf("embedded inputs/outputs wrong: %d/%d", len(inputs), len(outputs))
	}
}

func TestInputTestAction(t *testing.T) {
	srv := newServer(t)
	createJob(t, srv)

	code, _ := do(t, srv, http.MethodPut, jobBase+"job1/inputs/in1"+apiVer,
		`{"properties":{"type":"Stream"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create input = %d", code)
	}

	code, raw := do(t, srv, http.MethodPost, jobBase+"job1/inputs/in1/test"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("test status = %d, %s", code, raw)
	}

	if decode(t, raw)["status"] != "TestSucceeded" {
		t.Errorf("test result = %v", decode(t, raw)["status"])
	}
}

func TestDeleteChildThenJob(t *testing.T) {
	srv := newServer(t)
	createJob(t, srv)

	if code, _ := do(t, srv, http.MethodPut, jobBase+"job1/inputs/in1"+apiVer,
		`{"properties":{"type":"Stream"}}`); code != http.StatusCreated {
		t.Fatalf("create input")
	}

	if code, _ := do(t, srv, http.MethodDelete, jobBase+"job1/inputs/in1"+apiVer, ""); code != http.StatusOK {
		t.Errorf("delete input status != 200")
	}

	if code, _ := do(t, srv, http.MethodDelete, jobBase+"job1/inputs/in1"+apiVer, ""); code != http.StatusNoContent {
		t.Errorf("idempotent delete status != 204")
	}

	if code, _ := do(t, srv, http.MethodDelete, jobBase+"job1"+apiVer, ""); code != http.StatusOK {
		t.Errorf("delete job status != 200")
	}

	if code, _ := do(t, srv, http.MethodGet, jobBase+"job1"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", code)
	}
}

func TestChildUnderMissingJobParentNotFound(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPut, jobBase+"ghost/inputs/in1"+apiVer, `{"properties":{"type":"Stream"}}`)
	if code != http.StatusNotFound {
		t.Errorf("child under missing job status = %d, want 404", code)
	}
}

func TestCreateCompleteJobMaterializesChildren(t *testing.T) {
	srv := newServer(t)

	complete := `{
		"location": "West US",
		"properties": {
			"sku": {"name": "Standard"},
			"transformation": {"name": "t1", "properties": {"streamingUnits": 1, "query": "SELECT 1"}},
			"inputs": [{"name": "in1", "properties": {"type": "Stream"}}],
			"outputs": [{"name": "out1", "properties": {"datasource": {"type": "Microsoft.Storage/Blob"}}}]
		}
	}`

	code, _ := do(t, srv, http.MethodPut, jobBase+"jobc"+apiVer, complete)
	if code != http.StatusCreated {
		t.Fatalf("create-complete status = %d", code)
	}

	code, raw := do(t, srv, http.MethodGet, jobBase+"jobc/inputs/in1"+apiVer, "")
	if code != http.StatusOK {
		t.Errorf("embedded input not persisted: %d, %s", code, raw)
	}
}

func TestListJobs(t *testing.T) {
	srv := newServer(t)
	createJob(t, srv)

	code, raw := do(t, srv, http.MethodGet,
		"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.StreamAnalytics/streamingjobs"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}

	var out struct {
		Value []map[string]any `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	if len(out.Value) != 1 {
		t.Errorf("list count = %d, want 1", len(out.Value))
	}
}
