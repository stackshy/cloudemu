package managedkafka

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	mkprovider "github.com/stackshy/cloudemu/v2/providers/gcp/managedkafka"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

const loc = "/v1/projects/p/locations/us-central1"

func sharedHandler(t *testing.T) *Handler {
	t.Helper()

	mock := mkprovider.New(config.NewOptions(config.WithProjectID("p")))

	_, _, err := mock.CreateCluster(context.Background(), &mkdriver.Cluster{
		Project: "p", Location: "us-central1", ID: "owned",
		VcpuCount: 3, MemoryBytes: 3 << 30,
		Subnets: []string{"projects/p/regions/us-central1/subnetworks/s"},
	})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	h := New(mock)
	h.SetOperationRegistry(lro.NewRegistry())

	return h
}

func request(method, path, body string) *http.Request {
	r, _ := http.NewRequest(method, "http://x"+path, strings.NewReader(body))

	return r
}

func TestMatchesSharedClustersPath(t *testing.T) {
	h := sharedHandler(t)

	cases := []struct {
		name, method, path, body string
		want                     bool
	}{
		{"kafka create body", http.MethodPost, loc + "/clusters?clusterId=k", `{"capacityConfig":{}}`, true},
		{"kafka create gcpConfig only", http.MethodPost, loc + "/clusters?clusterId=k", `{"gcpConfig":{}}`, true},
		{"gke create body", http.MethodPost, loc + "/clusters", `{"cluster":{"name":"g"}}`, false},
		{"alloydb create body", http.MethodPost, loc + "/clusters?clusterId=a", `{"network":"n"}`, false},
		{"owned item", http.MethodGet, loc + "/clusters/owned", "", true},
		{"foreign item", http.MethodGet, loc + "/clusters/gke", "", false},
		{"list where owned", http.MethodGet, loc + "/clusters", "", true},
		{"list elsewhere", http.MethodGet, "/v1/projects/p/locations/europe-west1/clusters", "", false},
		{"topics under any cluster", http.MethodGet, loc + "/clusters/gke/topics", "", true},
		{"topic item", http.MethodDelete, loc + "/clusters/owned/topics/t", "", true},
		{"gke nodePools", http.MethodGet, loc + "/clusters/owned/nodePools", "", false},
		{"gke custom verb", http.MethodPost, loc + "/clusters/owned:setLogging", "", false},
		{"operation poll yields to lro", http.MethodGet, loc + "/operations/op-1", "", false},
		{"other collection", http.MethodGet, loc + "/instances", "", false},
		{"dataproc regions", http.MethodGet, "/v1/projects/p/regions/us-central1/clusters", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.Matches(request(tc.method, tc.path, tc.body)); got != tc.want {
				t.Fatalf("Matches(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestMatchesRestoresProbedBody guards the fall-through: a GKE create body that
// Kafka probed must still be fully readable by the next handler.
func TestMatchesRestoresProbedBody(t *testing.T) {
	h := sharedHandler(t)
	body := `{"cluster":{"name":"g"}}`
	r := request(http.MethodPost, loc+"/clusters", body)

	if h.Matches(r) {
		t.Fatalf("GKE body claimed by Kafka")
	}

	got, err := io.ReadAll(r.Body)
	if err != nil || string(got) != body {
		t.Fatalf("body after probe = %q (err %v), want %q", got, err, body)
	}
}

func TestMatchesStandaloneClaimsEverything(t *testing.T) {
	h := New(mkprovider.New(config.NewOptions(config.WithProjectID("p"))))

	for _, path := range []string{loc + "/clusters", loc + "/clusters/any", loc + "/operations/op-1"} {
		if !h.Matches(request(http.MethodGet, path, "")) {
			t.Fatalf("standalone handler should claim %s", path)
		}
	}
}
