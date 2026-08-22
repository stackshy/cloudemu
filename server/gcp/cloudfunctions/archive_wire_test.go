package cloudfunctions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpprovider "github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newArchiveServer wires a Cloud Functions server whose create() can resolve a
// gs:// sourceArchiveUrl from the in-process GCS backend.
func newArchiveServer(t *testing.T) (*httptest.Server, *recordingEngine, *gcpprovider.Provider) {
	t.Helper()

	eng := newRecordingEngine()
	cloud := cloudemu.NewGCP(config.WithFunctionEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
		CloudFunctions: cloud.CloudFunctions,
		Storage:        cloud.GCS,
	}))
	t.Cleanup(ts.Close)

	return ts, eng, cloud
}

func createFromArchive(t *testing.T, ts *httptest.Server, name, entryPoint, archiveURL string) int {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"entryPoint":       entryPoint,
		"runtime":          "python312",
		"sourceArchiveUrl": archiveURL,
	})

	resp, err := http.Post(ts.URL+functionsPath+"?functionId="+name, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}

func TestSourceArchiveURLRunsRealCode(t *testing.T) {
	ts, eng, cloud := newArchiveServer(t)
	ctx := context.Background()

	archive := []byte("archive-zip-bytes")
	if err := cloud.GCS.CreateBucket(ctx, "src"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := cloud.GCS.PutObject(ctx, "src", "fn.zip", archive, "application/zip", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if code := createFromArchive(t, ts, "arcfn", "hello", "gs://src/fn.zip"); code != http.StatusOK {
		t.Fatalf("create status = %d, want 200", code)
	}

	// The GCS object bytes must reach the engine (real code), under the http
	// framework contract gen1 uses — not a silent echo stub.
	if got := string(eng.deployed["arcfn"]); got != string(archive) {
		t.Fatalf("archive bytes did not reach the engine: got %q", got)
	}

	if eng.frames["arcfn"] != "http" {
		t.Fatalf("framework = %q, want http", eng.frames["arcfn"])
	}
}

func TestSourceArchiveURLMissingObjectFails(t *testing.T) {
	ts, _, cloud := newArchiveServer(t)

	if err := cloud.GCS.CreateBucket(context.Background(), "src"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// The bucket exists but the object does not: fail loudly, not a stub.
	if code := createFromArchive(t, ts, "arcfn", "hello", "gs://src/missing.zip"); code != http.StatusNotFound {
		t.Fatalf("create with missing archive = %d, want 404", code)
	}
}

func TestSourceArchiveURLMalformedRejected(t *testing.T) {
	ts, _, _ := newArchiveServer(t)

	if code := createFromArchive(t, ts, "arcfn", "hello", "https://example.com/fn.zip"); code != http.StatusBadRequest {
		t.Fatalf("create with non-gs archive = %d, want 400", code)
	}
}
