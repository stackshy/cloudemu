package cloudrun_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	project  = "demo-project"
	location = "us-central1"
)

// fakeEngine is a recording config.ContainerEngine wired into the provider so
// the wire path can be exercised without Docker.
type fakeEngine struct {
	ranSpecs []config.ContainerRunSpec
	statuses []config.ContainerStatus
	stopped  []string
}

//nolint:gocritic // spec is the by-value DTO the ContainerEngine contract defines.
func (f *fakeEngine) Run(_ context.Context, spec config.ContainerRunSpec) (string, error) {
	f.ranSpecs = append(f.ranSpecs, spec)

	return "handle-1", nil
}

func (f *fakeEngine) Status(_ context.Context, _ string) ([]config.ContainerStatus, error) {
	return f.statuses, nil
}

func (f *fakeEngine) Logs(_ context.Context, _, _ string, _ int) (string, error) { return "", nil }

func (f *fakeEngine) Exec(_ context.Context, _, _ string, _ []string) (config.ExecResult, error) {
	return config.ExecResult{}, nil
}

func (f *fakeEngine) Stop(_ context.Context, handle string) error {
	f.stopped = append(f.stopped, handle)

	return nil
}

func jobsURL(base string) string {
	return base + "/v2/projects/" + project + "/locations/" + location + "/jobs"
}

func newServer(t *testing.T, eng config.ContainerEngine) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP(config.WithContainerEngine(eng))
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudRun: cloud.CloudRun}))
	t.Cleanup(srv.Close)

	return srv
}

func postJSON(t *testing.T, url string, body any) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}

	resp, err := http.Post(url, "application/json", &buf)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	return decode(t, resp)
}

func getJSON(t *testing.T, url string) (map[string]any, int) {
	t.Helper()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}

	code := resp.StatusCode

	return decode(t, resp), code
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()

	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	var m map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("decode %q: %v", string(b), err)
		}
	}

	return m
}

func jobBody() map[string]any {
	return map[string]any{
		"template": map[string]any{
			"taskCount": 1,
			"template": map[string]any{
				"containers": []map[string]any{{
					"image":   "busybox",
					"command": []string{"echo"},
					"args":    []string{"hello"},
					"env":     []map[string]any{{"name": "MODE", "value": "batch"}},
				}},
			},
		},
	}
}

func TestJobsLifecycleOverWire(t *testing.T) {
	eng := &fakeEngine{statuses: []config.ContainerStatus{{Name: "", State: "exited", ExitCode: 0}}}
	srv := newServer(t, eng)

	// Create: POST .../jobs?jobId=batch returns an LRO with the Job inlined.
	createOp := postJSON(t, jobsURL(srv.URL)+"?jobId=batch", jobBody())
	if done, _ := createOp["done"].(bool); !done {
		t.Fatalf("create op not done: %+v", createOp)
	}

	jobResp, _ := createOp["response"].(map[string]any)
	if jobResp["@type"] != "type.googleapis.com/google.cloud.run.v2.Job" {
		t.Fatalf("create response @type = %v", jobResp["@type"])
	}

	wantName := "projects/" + project + "/locations/" + location + "/jobs/batch"
	if jobResp["name"] != wantName {
		t.Fatalf("job name = %v, want %v", jobResp["name"], wantName)
	}

	// Run: POST .../jobs/batch:run runs the real containers via the engine.
	runOp := postJSON(t, jobsURL(srv.URL)+"/batch:run", nil)
	if done, _ := runOp["done"].(bool); !done {
		t.Fatalf("run op not done: %+v", runOp)
	}

	if len(eng.ranSpecs) != 1 || !eng.ranSpecs[0].RunToCompletion {
		t.Fatalf("engine ran %d specs (RunToCompletion expected): %+v", len(eng.ranSpecs), eng.ranSpecs)
	}

	execResp, _ := runOp["response"].(map[string]any)
	if execResp["@type"] != "type.googleapis.com/google.cloud.run.v2.Execution" {
		t.Fatalf("run response @type = %v", execResp["@type"])
	}

	if got, _ := execResp["succeededCount"].(float64); got != 1 {
		t.Fatalf("succeededCount = %v, want 1", execResp["succeededCount"])
	}

	execName, _ := execResp["name"].(string)
	if execName == "" {
		t.Fatalf("execution name empty: %+v", execResp)
	}

	// Executions.get by full resource name.
	execGet, code := getJSON(t, srv.URL+"/v2/"+execName)
	if code != http.StatusOK || execGet["name"] != execName {
		t.Fatalf("get execution: code=%d body=%+v", code, execGet)
	}

	// Get + List job.
	if jr, code := getJSON(t, jobsURL(srv.URL)+"/batch"); code != http.StatusOK || jr["name"] != wantName {
		t.Fatalf("get job: code=%d body=%+v", code, jr)
	}

	listResp, code := getJSON(t, jobsURL(srv.URL))
	jobsArr, _ := listResp["jobs"].([]any)
	if code != http.StatusOK || len(jobsArr) != 1 {
		t.Fatalf("list jobs: code=%d body=%+v", code, listResp)
	}

	// Operation poll returns done.
	opName, _ := runOp["name"].(string)
	if op, code := getJSON(t, srv.URL+"/v2/"+opName); code != http.StatusOK {
		t.Fatalf("op poll code=%d", code)
	} else if done, _ := op["done"].(bool); !done {
		t.Fatalf("op poll not done: %+v", op)
	}

	// Delete stops the engine-backed workload.
	delReq, _ := http.NewRequest(http.MethodDelete, jobsURL(srv.URL)+"/batch", nil)

	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}

	delResp.Body.Close()

	if len(eng.stopped) != 1 || eng.stopped[0] != "handle-1" {
		t.Fatalf("engine Stop calls = %v, want [handle-1]", eng.stopped)
	}

	// Job is gone.
	if _, code := getJSON(t, jobsURL(srv.URL)+"/batch"); code != http.StatusNotFound {
		t.Fatalf("get after delete code = %d, want 404", code)
	}
}

func TestRunUnknownJobReturns404(t *testing.T) {
	srv := newServer(t, nil)

	body, code := getJSON(t, jobsURL(srv.URL)+"/ghost")
	if code != http.StatusNotFound {
		t.Fatalf("get unknown job code = %d body=%+v", code, body)
	}
}
