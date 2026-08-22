package realengine_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"google.golang.org/api/cloudfunctions/v1"
	"google.golang.org/api/option"
)

// gcfHTTPHandler is a gen1 Cloud Functions (functions-framework) HTTP handler:
// it takes a Flask-Request-like object, reads JSON off it, and returns a dict
// that Flask coerces to JSON. Doubling the input means a passing test can only
// mean the uploaded Python actually ran — not an echo. The source lives in
// main.py by the gen1 convention; the entrypoint is the bare name hello_http.
const gcfHTTPHandler = `def hello_http(request):
    body = request.get_json()
    return {"doubled": body["n"] * 2}
`

// TestGCPCloudFunctionsPythonE2E runs the real-user GCP Cloud Functions gen1
// deploy+invoke flow against CloudEmu backed by a real Python subprocess
// runtime (no Docker, no cloud account): GenerateUploadUrl → raw PUT of a
// python zip to the returned (same-server) URL → Create with SourceUploadUrl →
// Call and confirm the response is the uploaded handler actually executing →
// Delete and confirm the function is gone.
//
// Node gen1 uses the Express (req, res)=>res.json(...) contract, which needs a
// faked response object in the runner; that http-runner variant is a follow-up,
// so this e2e covers Python (the fully-supported http runtime) end to end.
func TestGCPCloudFunctionsPythonE2E(t *testing.T) {
	requireBinary(t, "python3")

	eng := realengine.NewSubprocess()

	cloud := cloudemu.NewGCP(config.WithFunctionEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))

	defer func() {
		ts.Close()
		_ = eng.Close()
	}()

	ctx := context.Background()

	svc, err := cloudfunctions.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	parent := "projects/demo/locations/us-central1"
	fqName := parent + "/functions/doubler"

	// 1. GenerateUploadUrl — now points back at this same server.
	up, err := svc.Projects.Locations.Functions.GenerateUploadUrl(parent,
		&cloudfunctions.GenerateUploadUrlRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GenerateUploadUrl: %v", err)
	}

	if up.UploadUrl == "" {
		t.Fatal("GenerateUploadUrl returned no uploadUrl")
	}

	// 2. PUT the real Python zip to the upload URL, GCS-signed-URL style.
	putSourceZip(t, up.UploadUrl, zipFile(t, "main.py", gcfHTTPHandler))

	// 3. Create with sourceUploadUrl set to the URL we PUT to.
	createOp, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:            fqName,
		Runtime:         "python312",
		EntryPoint:      "hello_http",
		SourceUploadUrl: up.UploadUrl,
		HttpsTrigger:    &cloudfunctions.HttpsTrigger{},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !createOp.Done {
		t.Fatal("Create operation not done")
	}

	// 4. Call with {"n": 21} → assert REAL execution doubled it to 42.
	resp, err := svc.Projects.Locations.Functions.Call(fqName,
		&cloudfunctions.CallFunctionRequest{Data: `{"n": 21}`}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if resp.Error != "" {
		t.Fatalf("Call returned function error %q, result %q", resp.Error, resp.Result)
	}

	if resp.Result != `{"doubled": 42}` {
		t.Fatalf("Result = %q, want {\"doubled\": 42} (proves the uploaded code ran)", resp.Result)
	}

	// 5. Delete, then confirm a subsequent Call fails.
	delOp, err := svc.Projects.Locations.Functions.Delete(fqName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatal("Delete operation not done")
	}

	if _, err := svc.Projects.Locations.Functions.Call(fqName,
		&cloudfunctions.CallFunctionRequest{Data: `{"n": 1}`}).Context(ctx).Do(); err == nil {
		t.Fatal("expected Call of the deleted function to fail")
	}
}

func putSourceZip(t *testing.T, uploadURL string, zipBytes []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, uploadURL, bytes.NewReader(zipBytes))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}

	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("x-goog-content-length-range", "0,104857600")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT source zip: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT source zip: status %d, want 200", res.StatusCode)
	}
}
