package cloudfunctions_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func patchFn(t *testing.T, ts *httptest.Server, name string, body map[string]string) int {
	t.Helper()

	b, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPatch, ts.URL+functionsPath+"/"+name, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new PATCH: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}

func callFn(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()

	resp, err := http.Post(ts.URL+functionsPath+"/"+name+":call", "application/json",
		bytes.NewReader([]byte(`{"data":"{}"}`)))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode call: %v", err)
	}

	if out.Error != "" {
		t.Fatalf("call returned error: %s", out.Error)
	}

	return out.Result
}

// TestUpdateFunctionRedeploysNewSource is the regression for the PATCH path that
// dropped the new source: create a function with code A, PATCH with a freshly
// uploaded code B, then invoke and confirm the engine runs B (the new real
// code), not the stale A.
func TestUpdateFunctionRedeploysNewSource(t *testing.T) {
	ts, eng := newUploadServer(t)

	// Deploy code A.
	uploadA := genUploadURL(t, ts)
	if code := put(t, uploadA, []byte("codeA")); code != http.StatusOK {
		t.Fatalf("PUT A status = %d", code)
	}

	if code := createFn(t, ts, "updfn", "hello", uploadA); code != http.StatusOK {
		t.Fatalf("create status = %d", code)
	}

	if got := callFn(t, ts, "updfn"); got != "codeA" {
		t.Fatalf("invoke after create returned %q, want codeA", got)
	}

	// PATCH with a freshly staged code B.
	uploadB := genUploadURL(t, ts)
	if code := put(t, uploadB, []byte("codeB")); code != http.StatusOK {
		t.Fatalf("PUT B status = %d", code)
	}

	if code := patchFn(t, ts, "updfn", map[string]string{"sourceUploadUrl": uploadB}); code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", code)
	}

	if got := string(eng.deployed["updfn"]); got != "codeB" {
		t.Fatalf("PATCH did not redeploy code B to the engine: got %q", got)
	}

	if got := callFn(t, ts, "updfn"); got != "codeB" {
		t.Fatalf("invoke after update returned %q, want codeB (stale codeA still running?)", got)
	}
}

// TestUpdateFunctionMetadataOnlyKeepsCode asserts a PATCH with no source leaves
// the deployed code untouched rather than wiping it.
func TestUpdateFunctionMetadataOnlyKeepsCode(t *testing.T) {
	ts, eng := newUploadServer(t)

	uploadA := genUploadURL(t, ts)
	if code := put(t, uploadA, []byte("codeA")); code != http.StatusOK {
		t.Fatalf("PUT A status = %d", code)
	}

	if code := createFn(t, ts, "metafn", "hello", uploadA); code != http.StatusOK {
		t.Fatalf("create status = %d", code)
	}

	// Metadata-only PATCH (new env var, no source).
	if code := patchFn(t, ts, "metafn", map[string]string{"runtime": "python312"}); code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", code)
	}

	if got := string(eng.deployed["metafn"]); got != "codeA" {
		t.Fatalf("metadata-only PATCH changed deployed code to %q, want codeA unchanged", got)
	}

	if got := callFn(t, ts, "metafn"); got != "codeA" {
		t.Fatalf("invoke after metadata update returned %q, want codeA", got)
	}
}

// TestUpdateFunctionUnknownTokenRejected asserts a PATCH naming an unknown
// upload token fails loudly rather than silently keeping stale code.
func TestUpdateFunctionUnknownTokenRejected(t *testing.T) {
	ts, _ := newUploadServer(t)

	uploadA := genUploadURL(t, ts)
	if code := put(t, uploadA, []byte("codeA")); code != http.StatusOK {
		t.Fatalf("PUT A status = %d", code)
	}

	if code := createFn(t, ts, "badfn", "hello", uploadA); code != http.StatusOK {
		t.Fatalf("create status = %d", code)
	}

	bogus := ts.URL + functionsPath + ":uploadSource?token=deadbeef"
	if code := patchFn(t, ts, "badfn", map[string]string{"sourceUploadUrl": bogus}); code != http.StatusBadRequest {
		t.Fatalf("patch with unknown token = %d, want 400", code)
	}
}
