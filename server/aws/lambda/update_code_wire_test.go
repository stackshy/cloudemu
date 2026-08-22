package lambda_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/aws/lambda"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return b
}

func putObj(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new PUT %s: %v", url, err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}

	return resp
}

// TestUpdateFunctionCodeDeploysNewCode is the regression for the missing `code`
// subresource: create a function with code A, PUT .../code with code B, then
// invoke and confirm the engine runs B (the new real code), not the stale A.
func TestUpdateFunctionCodeDeploysNewCode(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda))
	defer ts.Close()

	codeA := []byte(`ZIP-A`)
	codeB := []byte(`ZIP-B`)

	create := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "updfn",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"ZipFile": codeA},
	})
	create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}

	if got := eng.codeFor("updfn"); !bytes.Equal(got, codeA) {
		t.Fatalf("create did not deploy code A: got %q", got)
	}

	upd := putObj(t, ts.URL+"/2015-03-31/functions/updfn/code", mustJSON(t, map[string]any{"ZipFile": codeB}))
	defer upd.Body.Close()

	if upd.StatusCode != http.StatusOK {
		t.Fatalf("update-function-code status = %d, want 200", upd.StatusCode)
	}

	if got := eng.codeFor("updfn"); !bytes.Equal(got, codeB) {
		t.Fatalf("update-function-code did not redeploy code B to the engine: got %q", got)
	}

	// Invoke must now return code B's output, proving the new real code is live.
	inv := postObj(t, ts.URL+"/2015-03-31/functions/updfn/invocations", map[string]any{"ping": 1})
	defer inv.Body.Close()

	out, _ := io.ReadAll(inv.Body)
	if !bytes.Equal(out, codeB) {
		t.Fatalf("invoke after update returned %q, want code B output (stale A still running?)", out)
	}
}

// TestUpdateFunctionCodeFromS3RunsRealCode covers the S3-sourced update path:
// the new artifact is fetched from the in-process S3 backend and redeployed.
func TestUpdateFunctionCodeFromS3RunsRealCode(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda, lambda.WithObjectStore(cloud.S3)))
	defer ts.Close()

	create := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "s3upd",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"ZipFile": []byte("ZIP-A")},
	})
	create.Body.Close()

	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}

	newZip := makeZip(t, map[string]string{"main.py": "def handler(e, c):\n    return 'B'\n"})

	if err := cloud.S3.CreateBucket(t.Context(), "artifacts"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := cloud.S3.PutObject(t.Context(), "artifacts", "v2.zip", newZip, "application/zip", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	upd := putObj(t, ts.URL+"/2015-03-31/functions/s3upd/code",
		mustJSON(t, map[string]any{"S3Bucket": "artifacts", "S3Key": "v2.zip"}))
	defer upd.Body.Close()

	if upd.StatusCode != http.StatusOK {
		t.Fatalf("update-function-code from S3 status = %d, want 200", upd.StatusCode)
	}

	if got := eng.codeFor("s3upd"); !bytes.Equal(got, newZip) {
		t.Fatalf("S3 update bytes did not reach the engine: got %d bytes, want %d", len(got), len(newZip))
	}
}

// TestUpdateFunctionCodeMissingSourceFails asserts a code update with no
// deployment package fails loudly rather than reporting a no-op success.
func TestUpdateFunctionCodeMissingSourceFails(t *testing.T) {
	eng := newRecordEngine()
	cloud := cloudemu.NewAWS(config.WithFunctionEngine(eng))

	ts := httptest.NewServer(lambda.New(cloud.Lambda))
	defer ts.Close()

	create := postObj(t, ts.URL+"/2015-03-31/functions", map[string]any{
		"FunctionName": "nofn",
		"Runtime":      "python3.12",
		"Handler":      "main.handler",
		"Code":         map[string]any{"ZipFile": []byte("ZIP-A")},
	})
	create.Body.Close()

	upd := putObj(t, ts.URL+"/2015-03-31/functions/nofn/code", mustJSON(t, map[string]any{}))
	defer upd.Body.Close()

	if upd.StatusCode != http.StatusBadRequest {
		t.Fatalf("update-function-code with no source = %d, want 400", upd.StatusCode)
	}
}
