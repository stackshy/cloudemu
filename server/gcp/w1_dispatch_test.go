package gcp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doHost is do with an explicit Host header (none when host is empty).
func doHost(t *testing.T, ts *httptest.Server, method, host, path, body string) (int, string) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, ts.URL+path, rdr) //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if host != "" {
		req.Host = host
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

func mustOK(t *testing.T, code int, body, what string) {
	t.Helper()

	if code != http.StatusOK {
		t.Fatalf("%s: code=%d body=%s", what, code, body)
	}
}

// TestKafkaListWithHint (GMK-01): with a GKE and a Kafka cluster in the same
// location, the hinted list reaches the hinted service. Unhinted stays GKE's.
func TestKafkaListWithHint(t *testing.T) {
	ts := fullServer(t)

	const base = "/v1/projects/demo/locations/us-central1/clusters"

	code, body := do(t, ts, http.MethodPost, base, `{"cluster":{"name":"c1","initialNodeCount":1}}`)
	mustOK(t, code, body, "GKE create")

	code, body = do(t, ts, http.MethodPost, base+"?clusterId=k3", goldenKafkaBody)
	mustOK(t, code, body, "Kafka create")

	cases := []struct {
		name, host, path, want, notWant string
	}{
		{"host managedkafka", "managedkafka.googleapis.com", base, "clusters/k3", "c1"},
		{"alias managedkafka", "", "/managedkafka.googleapis.com" + base, "clusters/k3", "c1"},
		{"alias container", "", "/container.googleapis.com" + base, "c1", "clusters/k3"},
		{"unhinted stays GKE", "", base, "c1", "clusters/k3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doHost(t, ts, http.MethodGet, tc.host, tc.path, "")

			if code != http.StatusOK || !strings.Contains(body, tc.want) || strings.Contains(body, tc.notWant) {
				t.Fatalf("code=%d body=%s, want %q and not %q", code, body, tc.want, tc.notWant)
			}
		})
	}
}

// TestRedisVisibleBesideFilestore (GMEM-07, hint part): with a Filestore and a
// Redis instance in one location, the redis-hinted list returns the Redis one.
func TestRedisVisibleBesideFilestore(t *testing.T) {
	ts := fullServer(t)

	const base = "/v1/projects/demo/locations/us-central1/instances"

	code, body := do(t, ts, http.MethodPost, base+"?instanceId=f1", goldenFilestoreBody)
	mustOK(t, code, body, "Filestore create")

	code, body = do(t, ts, http.MethodPost, base+"?instanceId=r1", goldenRedisBody)
	mustOK(t, code, body, "Redis create")

	for _, tc := range []struct{ name, host, path string }{
		{"host", "redis.googleapis.com", base},
		{"alias", "", "/redis.googleapis.com" + base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doHost(t, ts, http.MethodGet, tc.host, tc.path, "")

			if code != http.StatusOK || !strings.Contains(body, "instances/r1") || strings.Contains(body, "instances/f1") {
				t.Fatalf("code=%d body=%s, want r1 only", code, body)
			}
		})
	}

	code, body = do(t, ts, http.MethodGet, "/file.googleapis.com"+base, "")
	if code != http.StatusOK || !strings.Contains(body, "instances/f1") || strings.Contains(body, "instances/r1") {
		t.Fatalf("file-hinted list: code=%d body=%s, want f1 only", code, body)
	}
}

// TestDataFusionHintedDelete404 (GDFU-03, hint part): a hinted DELETE of a
// missing Data Fusion instance gets Data Fusion's 404, not Memorystore's.
func TestDataFusionHintedDelete404(t *testing.T) {
	ts := fullServer(t)

	const item = "/v1/projects/demo/locations/us-central1/instances/nope"

	for _, tc := range []struct{ name, host, path string }{
		{"host", "datafusion.googleapis.com", item},
		{"alias", "", "/datafusion.googleapis.com" + item},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doHost(t, ts, http.MethodDelete, tc.host, tc.path, "")

			if code != http.StatusNotFound || strings.Contains(body, "cache") || strings.Contains(body, "bucket") {
				t.Fatalf("code=%d body=%s, want a Data Fusion 404", code, body)
			}
		})
	}
}
