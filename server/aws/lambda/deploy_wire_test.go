package lambda_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/aws/lambda"
)

// recordEngine is a config.FunctionEngine that records the code deployed for
// each function and returns a canned invoke result, so the wire path can be
// tested without a real runtime. A non-echo canned result proves the invoke
// went through the engine (real code) rather than the stub echo.
type recordEngine struct {
	mu       sync.Mutex
	deployed map[string][]byte
	result   config.FunctionResult
}

func newRecordEngine() *recordEngine {
	return &recordEngine{deployed: map[string][]byte{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (e *recordEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.deployed[fn.Name] = fn.Code

	return nil
}

func (e *recordEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	if len(e.result.Payload) > 0 {
		return e.result, nil
	}

	// With no canned result, echo the currently-deployed package so a test can
	// observe which code version is live after an update.
	e.mu.Lock()
	defer e.mu.Unlock()

	return config.FunctionResult{Payload: e.deployed[name]}, nil
}

func (e *recordEngine) Remove(_ context.Context, _ string) error { return nil }

func (e *recordEngine) codeFor(name string) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.deployed[name]
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}

		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}

func zipNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open merged zip: %v", err)
	}

	names := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		names[f.Name] = true
	}

	return names
}

func postObj(t *testing.T, url string, body any) *http.Response {
	t.Helper()

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	return resp
}

func TestCreateFunctionFromS3RunsRealCode(t *testing.T) {
	eng := newRecordEngine()
	eng.result = config.FunctionResult{Payload: []byte(`{"from":"engine"}`)}
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda, lambda.WithObjectStore(cloud.S3)))
	defer ts.Close()

	ctx := context.Background()
	zipBytes := makeZip(t, map[string]string{"main.py": "def handler(e, c):\n    return e\n"})

	if err := cloud.S3.CreateBucket(ctx, "artifacts"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := cloud.S3.PutObject(ctx, "artifacts", "fn.zip", zipBytes, "application/zip", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	resp := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "s3fn",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"S3Bucket": "artifacts", "S3Key": "fn.zip"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}

	if got := eng.codeFor("s3fn"); !bytes.Equal(got, zipBytes) {
		t.Fatalf("S3 bytes did not reach the engine: got %d bytes, want %d", len(got), len(zipBytes))
	}

	// Invoke must return the engine's payload, proving real code ran instead of
	// the echo stub (which would return the request payload unchanged).
	inv := postObj(t, ts.URL+"/2015-03-31/functions/s3fn/invocations", map[string]any{"ping": 1})
	defer inv.Body.Close()

	out, _ := io.ReadAll(inv.Body)
	if string(out) != `{"from":"engine"}` {
		t.Fatalf("invoke returned %q, want engine payload (not echo)", string(out))
	}
}

func TestCreateFunctionFromS3FailsLoudlyWithoutBackend(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	// No object store wired: an S3-sourced deploy must fail, not silently stub.
	ts := httptest.NewServer(lambda.New(cloud.Lambda))
	defer ts.Close()

	resp := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "s3fn",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"S3Bucket": "artifacts", "S3Key": "fn.zip"},
	})
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		t.Fatal("create succeeded without an S3 backend; want a loud failure")
	}
}

func TestCreateFunctionFromS3MissingObjectFails(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda, lambda.WithObjectStore(cloud.S3)))
	defer ts.Close()

	resp := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "s3fn",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"S3Bucket": "artifacts", "S3Key": "missing.zip"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("create with missing S3 object = %d, want 404", resp.StatusCode)
	}
}

func TestCreateFunctionWithCodeLayerOverlaysContent(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda))
	defer ts.Close()

	// A real-shaped python layer keeps modules under python/; the overlay strips
	// that root so the module lands alongside the function code.
	layerZip := makeZip(t, map[string]string{"python/greeting.py": "def hello():\n    return 'hi'\n"})

	pub := postObj(t, ts.URL+"/2018-10-31/layers/deps/versions", map[string]any{
		"Description":        "deps",
		"Content":            map[string]any{"ZipFile": layerZip},
		"CompatibleRuntimes": []string{"python3.12"},
	})

	var pubResp struct {
		LayerVersionArn string `json:"LayerVersionArn"`
	}

	_ = json.NewDecoder(pub.Body).Decode(&pubResp)
	pub.Body.Close()

	if pubResp.LayerVersionArn == "" {
		t.Fatal("PublishLayerVersion returned no LayerVersionArn")
	}

	fnZip := makeZip(t, map[string]string{"main.py": "import greeting\n"})

	create := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "layered",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"ZipFile": fnZip},
		"Layers":       []string{pubResp.LayerVersionArn},
	})
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}

	merged := eng.codeFor("layered")
	if len(merged) == 0 {
		t.Fatal("no deployment package reached the engine")
	}

	names := zipNames(t, merged)
	if !names["main.py"] {
		t.Fatalf("merged package missing function code, has: %v", names)
	}

	if !names["greeting.py"] {
		t.Fatalf("merged package missing overlaid layer module (python/ should be stripped), has: %v", names)
	}
}

func TestCreateNodeFunctionWithCodeLayerKeepsNodeModules(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda))
	defer ts.Close()

	// A real-shaped Node layer keeps packages under nodejs/node_modules/. Only the
	// "nodejs/" root is stripped, so the package lands at node_modules/<pkg>/ where
	// Node's own resolver finds it from the function dir (no NODE_PATH needed).
	layerZip := makeZip(t, map[string]string{
		"nodejs/node_modules/leftpad/index.js": "module.exports = () => 'x'\n",
	})

	pub := postObj(t, ts.URL+"/2018-10-31/layers/nodedeps/versions", map[string]any{
		"Description":        "nodedeps",
		"Content":            map[string]any{"ZipFile": layerZip},
		"CompatibleRuntimes": []string{"nodejs20.x"},
	})

	var pubResp struct {
		LayerVersionArn string `json:"LayerVersionArn"`
	}

	_ = json.NewDecoder(pub.Body).Decode(&pubResp)
	pub.Body.Close()

	if pubResp.LayerVersionArn == "" {
		t.Fatal("PublishLayerVersion returned no LayerVersionArn")
	}

	fnZip := makeZip(t, map[string]string{"index.js": "const leftpad = require('leftpad')\n"})

	create := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "nodelayered",
		"Runtime":      "nodejs20.x",
		"Handler":      "index.handler",
		"Code":         map[string]any{"ZipFile": fnZip},
		"Layers":       []string{pubResp.LayerVersionArn},
	})
	defer create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}

	names := zipNames(t, eng.codeFor("nodelayered"))
	if !names["node_modules/leftpad/index.js"] {
		t.Fatalf("Node layer module must land at node_modules/leftpad/index.js (only nodejs/ stripped), has: %v", names)
	}
}
