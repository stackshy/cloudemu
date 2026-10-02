package gcp_test

import (
	"net/http"
	"strings"
	"testing"
)

const w1aLoc = "/v1/projects/demo/locations/us-central1"

func wantCode(t *testing.T, what string, code int, body string, want int, marker string) {
	t.Helper()

	if code != want || (marker != "" && !strings.Contains(body, marker)) {
		t.Fatalf("%s: code=%d body=%.300s, want %d containing %q", what, code, body, want, marker)
	}
}

// TestServeMountsAlloyDBBesideGKE (GX-01/GADB-01): the assembled server mounts
// AlloyDB beside GKE, and both keep their own traffic.
func TestServeMountsAlloyDBBesideGKE(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/clusters?clusterId=ca", goldenAlloyBody)
	wantCode(t, "AlloyDB create", code, body, http.StatusOK, "databaseVersion")

	code, body = do(t, ts, http.MethodGet, "/alloydb.googleapis.com"+w1aLoc+"/clusters/ca", "")
	wantCode(t, "hinted AlloyDB get", code, body, http.StatusOK, "databaseVersion")

	code, body = do(t, ts, http.MethodGet, "/alloydb.googleapis.com"+w1aLoc+"/clusters", "")
	wantCode(t, "hinted AlloyDB list", code, body, http.StatusOK, "clusters/ca")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/backups", "")
	wantCode(t, "AlloyDB backups list", code, body, http.StatusOK, "")

	// Terraform reads these back: networkConfig, instance labels, and no
	// gceZone on a REGIONAL instance, or every plan shows drift.
	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/ca", "")
	wantCode(t, "AlloyDB get networkConfig", code, body, http.StatusOK, `"networkConfig":{"network":"projects/demo/global/networks/default"}`)

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters/ca/instances?instanceId=i1", `{"instanceType":"PRIMARY","labels":{"env":"one"}}`)
	wantCode(t, "AlloyDB instance create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/ca/instances/i1", "")
	if code != http.StatusOK || !strings.Contains(body, `"labels":{"env":"one"}`) || strings.Contains(body, "gceZone") {
		t.Fatalf("AlloyDB instance get: code=%d body=%.300s, want labels and no gceZone", code, body)
	}

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters", `{"cluster":{"name":"g1","initialNodeCount":1}}`)
	wantCode(t, "GKE create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters", "")
	if code != http.StatusOK || !strings.Contains(body, "g1") || strings.Contains(body, "clusters/ca") {
		t.Fatalf("unhinted list: code=%d body=%.300s, want GKE g1 only", code, body)
	}
}

// TestUnhintedAlloyDBItemsReachAlloyDB (R3-i): the create, read, label update
// and delete a Terraform run issues all reach AlloyDB without a hint, and a GKE
// cluster in the same location is unaffected.
func TestUnhintedAlloyDBItemsReachAlloyDB(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/clusters", `{"cluster":{"name":"g1","initialNodeCount":1}}`)
	wantCode(t, "GKE create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters?clusterId=a1", goldenAlloyBody)
	wantCode(t, "AlloyDB create", code, body, http.StatusOK, "databaseVersion")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/a1", "")
	wantCode(t, "AlloyDB get", code, body, http.StatusOK, "databaseVersion")

	// Kafka registers first and claims labels-only PATCHes of ids nobody owns;
	// it must see the AlloyDB id (F1).
	code, body = do(t, ts, http.MethodPatch, w1aLoc+"/clusters/a1?updateMask=labels", `{"labels":{"env":"two"}}`)
	wantCode(t, "AlloyDB label update", code, body, http.StatusOK, "databaseVersion")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/a1", "")
	wantCode(t, "AlloyDB get after update", code, body, http.StatusOK, `"two"`)

	code, body = do(t, ts, http.MethodDelete, w1aLoc+"/clusters/a1", "")
	wantCode(t, "AlloyDB delete", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, "/alloydb.googleapis.com"+w1aLoc+"/clusters/a1", "")
	wantCode(t, "AlloyDB get after delete", code, body, http.StatusNotFound, "")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/g1", "")
	wantCode(t, "GKE get", code, body, http.StatusOK, `"initialNodeCount"`)

	// Residual: a GKE cluster is deleted, then an AlloyDB cluster takes its name.
	code, body = do(t, ts, http.MethodDelete, w1aLoc+"/clusters/g1", "")
	wantCode(t, "GKE delete", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters?clusterId=g1", goldenAlloyBody)
	wantCode(t, "AlloyDB create over deleted GKE name", code, body, http.StatusOK, "databaseVersion")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/g1", "")
	wantCode(t, "GET reused name", code, body, http.StatusOK, "databaseVersion")
}

// TestSharedClustersCollisions (R2-i, R2-iv, F2): GKE and AlloyDB cannot hold
// the same cluster name, lists stay with GKE, and Kafka keeps its own location.
func TestSharedClustersCollisions(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/clusters", `{"cluster":{"name":"main","initialNodeCount":1}}`)
	wantCode(t, "GKE create main", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters?clusterId=main", goldenAlloyBody)
	wantCode(t, "AlloyDB create over GKE main", code, body, http.StatusConflict, "ALREADY_EXISTS")

	code, body = do(t, ts, http.MethodPost, "/v1/projects/demo/locations/us-east1/clusters?clusterId=main", goldenAlloyBody)
	wantCode(t, "AlloyDB create over GKE main, other region", code, body, http.StatusConflict, "ALREADY_EXISTS")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters?clusterId=a1", goldenAlloyBody)
	wantCode(t, "AlloyDB create a1", code, body, http.StatusOK, "databaseVersion")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/clusters", `{"cluster":{"name":"a1","initialNodeCount":1}}`)
	wantCode(t, "GKE create over AlloyDB a1", code, body, http.StatusConflict, "ALREADY_EXISTS")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/main", "")
	wantCode(t, "GKE main unaffected", code, body, http.StatusOK, `"initialNodeCount"`)

	west := strings.ReplaceAll(goldenKafkaBody, "us-central1", "europe-west1")
	code, body = do(t, ts, http.MethodPost, "/v1/projects/demo/locations/europe-west1/clusters?clusterId=k2", west)
	wantCode(t, "Kafka create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, "/v1/projects/demo/locations/europe-west1/clusters", "")
	wantCode(t, "europe-west1 list", code, body, http.StatusOK, "clusters/k2")

	code, body = do(t, ts, http.MethodGet, "/v1/projects/demo/locations/asia-east1/clusters", "")
	if code != http.StatusOK || strings.Contains(body, "a1") {
		t.Fatalf("empty-location list: code=%d body=%.300s, want GKE empty list", code, body)
	}

	code, body = do(t, ts, http.MethodGet, "/v1/projects/demo/locations/-/clusters", "")
	if code != http.StatusOK || !strings.Contains(body, `"main"`) || strings.Contains(body, "clusters/a1") {
		t.Fatalf("locations/- list: code=%d body=%.300s, want GKE main only", code, body)
	}
}

// TestGKEUnknownSubresource404 (GKE-09): an unknown cluster sub-resource is not
// answered with the cluster body.
func TestGKEUnknownSubresource404(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/clusters", `{"cluster":{"name":"c1","initialNodeCount":1}}`)
	wantCode(t, "GKE create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/c1/bogus", "")
	if code != http.StatusNotFound || strings.Contains(body, "initialNodeCount") {
		t.Fatalf("bogus sub-resource: code=%d body=%.300s, want 404 without the cluster", code, body)
	}

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/clusters/c1/jwks", "")
	wantCode(t, "jwks", code, body, http.StatusNotImplemented, "UNIMPLEMENTED")
}
