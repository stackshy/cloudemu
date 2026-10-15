package gcs_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func bucketCall(t *testing.T, ts *httptest.Server, method, query, body string) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequest(method, ts.URL+"/storage/v1/b"+query, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)

	return resp.StatusCode, out
}

func listedNames(out map[string]any) []string {
	items, _ := out["items"].([]any)
	names := make([]string, 0, len(items))

	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			names = append(names, m["name"].(string))
		}
	}

	return names
}

// TestProjectScopeBuckets guards the GCS project rules: bucket names are global
// (a create in another project conflicts), buckets.list returns only the named
// project's buckets, and buckets.list without project is a 400.
func TestProjectScopeBuckets(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS}))
	t.Cleanup(ts.Close)

	for _, c := range []struct{ project, name string }{{"p-a", "bkt-a"}, {"p-b", "bkt-b"}} {
		if code, _ := bucketCall(t, ts, http.MethodPost, "?project="+c.project, `{"name":"`+c.name+`"}`); code != http.StatusOK {
			t.Fatalf("create %s in %s: status %d", c.name, c.project, code)
		}
	}

	if code, _ := bucketCall(t, ts, http.MethodPost, "?project=p-b", `{"name":"bkt-a"}`); code != http.StatusConflict {
		t.Errorf("cross-project create of bkt-a: status %d, want 409", code)
	}

	tests := []struct {
		project string
		want    string
	}{
		{"p-a", "bkt-a"},
		{"p-b", "bkt-b"},
	}

	for _, tc := range tests {
		code, out := bucketCall(t, ts, http.MethodGet, "?project="+tc.project, "")
		if code != http.StatusOK {
			t.Fatalf("list %s: status %d", tc.project, code)
		}

		if got := listedNames(out); len(got) != 1 || got[0] != tc.want {
			t.Errorf("list %s = %v, want [%s]", tc.project, got, tc.want)
		}
	}

	if code, _ := bucketCall(t, ts, http.MethodGet, "", ""); code != http.StatusBadRequest {
		t.Errorf("list without project: status %d, want 400", code)
	}
}
