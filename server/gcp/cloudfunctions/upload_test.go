package cloudfunctions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// recordingEngine is a config.FunctionEngine that records deployments so the
// upload-staging wire path can be tested without a real runtime.
type recordingEngine struct {
	mu       sync.Mutex
	deployed map[string][]byte
	frames   map[string]string
}

func newRecordingEngine() *recordingEngine {
	return &recordingEngine{deployed: map[string][]byte{}, frames: map[string]string{}}
}

//nolint:gocritic // fn is the by-value DTO defined by the FunctionEngine contract
func (e *recordingEngine) Deploy(_ context.Context, fn config.FunctionDeployment) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.deployed[fn.Name] = fn.Code
	e.frames[fn.Name] = fn.Framework

	return nil
}

func (e *recordingEngine) Invoke(_ context.Context, name string, _ []byte) (config.FunctionResult, error) {
	// Echo the currently-deployed package so a test can observe which code
	// version is live after an update.
	e.mu.Lock()
	defer e.mu.Unlock()

	return config.FunctionResult{Payload: e.deployed[name]}, nil
}

func (e *recordingEngine) Remove(_ context.Context, _ string) error { return nil }

const functionsPath = "/v1/projects/p1/locations/us-central1/functions"

func newUploadServer(t *testing.T) (*httptest.Server, *recordingEngine) {
	t.Helper()

	eng := newRecordingEngine()
	cloud := cloudemu.NewGCP(config.WithFunctionEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))
	t.Cleanup(ts.Close)

	return ts, eng
}

func genUploadURL(t *testing.T, ts *httptest.Server) string {
	t.Helper()

	resp, err := http.Post(ts.URL+functionsPath+":generateUploadUrl", "application/json", nil)
	if err != nil {
		t.Fatalf("generateUploadUrl: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.UploadURL == "" {
		t.Fatal("empty uploadUrl")
	}

	return out.UploadURL
}

func put(t *testing.T, url string, body []byte) int {
	t.Helper()

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new PUT: %v", err)
	}

	req.Header.Set("Content-Type", "application/zip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}

func createFn(t *testing.T, ts *httptest.Server, name, entryPoint, uploadURL string) int {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"entryPoint":      entryPoint,
		"runtime":         "python312",
		"sourceUploadUrl": uploadURL,
	})

	resp, err := http.Post(ts.URL+functionsPath+"?functionId="+name, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}

func TestUploadStagingHappyPath(t *testing.T) {
	ts, eng := newUploadServer(t)

	uploadURL := genUploadURL(t, ts)
	if code := put(t, uploadURL, []byte("zip-bytes")); code != http.StatusOK {
		t.Fatalf("PUT status = %d", code)
	}

	if code := createFn(t, ts, "fn1", "hello", uploadURL); code != http.StatusOK {
		t.Fatalf("create status = %d", code)
	}

	if got := string(eng.deployed["fn1"]); got != "zip-bytes" {
		t.Fatalf("deployed code = %q", got)
	}

	if eng.frames["fn1"] != "http" {
		t.Fatalf("framework = %q, want http", eng.frames["fn1"])
	}
}

func TestUploadUnknownTokenRejected(t *testing.T) {
	ts, _ := newUploadServer(t)

	// A PUT to a token this server never minted is a 404.
	bogus := ts.URL + functionsPath + ":uploadSource?token=deadbeef"
	if code := put(t, bogus, []byte("x")); code != http.StatusNotFound {
		t.Fatalf("PUT unknown token = %d, want 404", code)
	}

	// A create referencing a token with no staged bytes is a 400, not a silent
	// stub-backed function.
	if code := createFn(t, ts, "fn1", "hello", bogus); code != http.StatusBadRequest {
		t.Fatalf("create unknown token = %d, want 400", code)
	}
}

func TestUploadDoubleConsumeRejected(t *testing.T) {
	ts, _ := newUploadServer(t)

	uploadURL := genUploadURL(t, ts)
	if code := put(t, uploadURL, []byte("zip-bytes")); code != http.StatusOK {
		t.Fatalf("PUT status = %d", code)
	}

	if code := createFn(t, ts, "fn1", "hello", uploadURL); code != http.StatusOK {
		t.Fatalf("first create = %d", code)
	}

	// The token was consumed by the first create; a second create with the same
	// URL must fail rather than reuse stale bytes.
	if code := createFn(t, ts, "fn2", "hello", uploadURL); code != http.StatusBadRequest {
		t.Fatalf("second create = %d, want 400", code)
	}
}

func TestUploadMissingEntryPointRejected(t *testing.T) {
	ts, _ := newUploadServer(t)

	uploadURL := genUploadURL(t, ts)
	if code := put(t, uploadURL, []byte("zip-bytes")); code != http.StatusOK {
		t.Fatalf("PUT status = %d", code)
	}

	if code := createFn(t, ts, "fn1", "", uploadURL); code != http.StatusBadRequest {
		t.Fatalf("create without entryPoint = %d, want 400", code)
	}
}
