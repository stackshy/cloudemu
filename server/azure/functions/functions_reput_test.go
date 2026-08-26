package functions_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestFunctionRePUTPreservesKeysAnd200 verifies, at the wire level, the fix for
// the audit finding that a function PUT-as-update rotated the default key and
// always answered 201: the first PUT creates (201) and the re-PUT updates (200)
// while keeping the same key. The status codes are observed with a raw client
// because the generated SDK's BeginCreateFunction only accepts 201.
func TestFunctionRePUTPreservesKeysAnd200(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	hc := ts.Client()
	ctx := context.Background()
	base := ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.Web/sites/reput-app"

	// The site must exist before a function can be deployed to it.
	if st := doJSON(t, ctx, hc, http.MethodPut, base, `{"location":"eastus","properties":{"siteConfig":{}}}`); st != http.StatusOK {
		t.Fatalf("site create status = %d, want 200", st)
	}

	fnURL := base + "/functions/fn1"

	if st := doJSON(t, ctx, hc, http.MethodPut, fnURL, `{"properties":{"config":{}}}`); st != http.StatusCreated {
		t.Fatalf("first function PUT status = %d, want 201", st)
	}

	key1 := functionDefaultKey(t, ctx, hc, fnURL+"/listkeys")
	if key1 == "" {
		t.Fatal("function default key empty after create")
	}

	// Re-PUT the same function: an update, so 200 and the key is unchanged.
	if st := doJSON(t, ctx, hc, http.MethodPut, fnURL, `{"properties":{"config":{}}}`); st != http.StatusOK {
		t.Fatalf("re-PUT function status = %d, want 200", st)
	}

	key2 := functionDefaultKey(t, ctx, hc, fnURL+"/listkeys")
	if key2 != key1 {
		t.Fatalf("re-PUT rotated the function key: %q -> %q", key1, key2)
	}
}

func doJSON(t *testing.T, ctx context.Context, hc *http.Client, method, url, body string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}

func functionDefaultKey(t *testing.T, ctx context.Context, hc *http.Client, url string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		t.Fatalf("new listkeys request: %v", err)
	}

	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("listkeys: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listkeys status = %d, want 200", resp.StatusCode)
	}

	var out struct {
		Properties map[string]string `json:"properties"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode listkeys: %v", err)
	}

	return out.Properties["default"]
}
