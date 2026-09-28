package gcp_test

// Regression coverage for the operations-ownership bug: a POST or DELETE
// (operations.cancel / operations.delete) against the shared location
// operations path with an UNKNOWN operation name used to fall through the
// shared lro handler (which only claimed GET) onto whichever of
// artifactregistry / eventarc / memorystore / alloydb registered next, and
// each of those greedily answered `{"done":true}` for ANY operation id
// regardless of whether it created it. Real GCP 404s an operation name that
// doesn't exist, on every verb. These tests drive the FULL assembled server
// (as `cloudemu serve` does) so the actual dispatch order, not just the lro
// package in isolation, is what's under test.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestFullServerBogusOperationIs404OnEveryVerb is the core regression: GET,
// POST :cancel, and DELETE on an operation name nobody ever created must all
// 404, not fabricate success.
func TestFullServerBogusOperationIs404OnEveryVerb(t *testing.T) {
	ts := fullServer(t)

	const base = "/v1/projects/demo/locations/us/operations/never-created-this-op"

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"get", http.MethodGet, base},
		{"cancel", http.MethodPost, base + ":cancel"},
		{"delete", http.MethodDelete, base},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := do(t, ts, tc.method, tc.path, "")
			if code != http.StatusNotFound {
				t.Fatalf("%s %s = %d %s, want 404 NOT_FOUND", tc.method, tc.path, code, body)
			}

			if strings.Contains(body, `"done":true`) {
				t.Fatalf("%s %s fabricated success instead of 404: %s", tc.method, tc.path, body)
			}
		})
	}
}

// opName reads the "name" field out of a create response body.
func opName(t *testing.T, body string) string {
	t.Helper()

	var v struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Name == "" {
		t.Fatalf("cannot read operation name from %s (err=%v)", body, err)
	}

	return v.Name
}

// TestFullServerRealOperationsStillResolveAfterFix guards the fix's main
// regression risk: closing the fake-success hole must not break polling a
// REAL operation. artifactregistry and memorystore are a representative
// subset of the four operations-minting handlers. They share the identical
// doneOperation -> h.ops.Register mechanism eventarc and alloydb use (see
// server/gcp/{eventarc,alloydb} which the standalone Matches-gating tests in
// each package cover directly).
func TestFullServerRealOperationsStillResolveAfterFix(t *testing.T) {
	ts := fullServer(t)

	// artifactregistry: create, poll (done), cancel (no-op success on an
	// already-done op, matching real GCP), then delete (removes the record so
	// a later poll 404s).
	_, createBody := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/repositories?repositoryId=r1", `{"format":"MAVEN"}`)
	arOp := "/v1/" + opName(t, createBody)

	if code, body := do(t, ts, http.MethodGet, arOp, ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("AR op GET: code=%d body=%s (want 200 done:true)", code, body)
	}

	if code, body := do(t, ts, http.MethodPost, arOp+":cancel", ""); code != http.StatusOK {
		t.Fatalf("AR op cancel: code=%d body=%s (want 200)", code, body)
	}

	if code, body := do(t, ts, http.MethodDelete, arOp, ""); code != http.StatusOK {
		t.Fatalf("AR op delete: code=%d body=%s (want 200)", code, body)
	}

	if code, _ := do(t, ts, http.MethodGet, arOp, ""); code != http.StatusNotFound {
		t.Fatalf("AR op GET after delete: code=%d (want 404)", code)
	}

	// memorystore: create and poll (done). Only Get is exercised on this one
	// (cancel/delete already proven end to end above via artifactregistry;
	// the shared lro handler applies identically to every registered name).
	_, msBody := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/instances?instanceId=cache1", `{}`)
	msOp := "/v1/" + opName(t, msBody)

	if code, body := do(t, ts, http.MethodGet, msOp, ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("Memorystore op GET: code=%d body=%s (want 200 done:true)", code, body)
	}
}

// TestAlloyDBServerOperationOwnership covers the fourth handler (AlloyDB),
// which can't be enabled alongside GKE in fullServer (identical REST paths),
// so it gets its own minimal server via DriversFromWithAlloyDB.
func TestAlloyDBServerOperationOwnership(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFromWithAlloyDB(cloud))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// A bogus operation 404s on every verb.
	const base = "/v1/projects/demo/locations/us/operations/never-created-this-op"

	if code, _ := do(t, ts, http.MethodGet, base, ""); code != http.StatusNotFound {
		t.Fatalf("bogus op GET: code=%d, want 404", code)
	}

	if code, _ := do(t, ts, http.MethodPost, base+":cancel", ""); code != http.StatusNotFound {
		t.Fatalf("bogus op cancel: code=%d, want 404", code)
	}

	if code, _ := do(t, ts, http.MethodDelete, base, ""); code != http.StatusNotFound {
		t.Fatalf("bogus op delete: code=%d, want 404", code)
	}

	// A real AlloyDB cluster-create operation still resolves.
	_, createBody := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us/clusters?clusterId=c1", `{}`)
	op := "/v1/" + opName(t, createBody)

	if code, body := do(t, ts, http.MethodGet, op, ""); code != http.StatusOK || !strings.Contains(body, `"done":true`) {
		t.Fatalf("AlloyDB op GET: code=%d body=%s (want 200 done:true)", code, body)
	}
}

// TestFullServerManagedKafkaSharesClustersWithGKE covers the one location
// collection two services claim on the same assembled server: Managed Kafka and
// GKE both serve /v1/projects/{p}/locations/{l}/clusters. Kafka registers first
// and claims only its own traffic, so GKE creates/lists still reach GKE, Kafka
// clusters are served by Kafka, and a Kafka cluster operation is resolved by the
// shared LRO poller (with its typed response), not by GKE's operations route.
func TestFullServerManagedKafkaSharesClustersWithGKE(t *testing.T) {
	ts := fullServer(t)

	const base = "/v1/projects/demo/locations/us-central1/clusters"

	// GKE create (a CreateClusterRequest wrapping {"cluster": …}) reaches GKE.
	if code, body := do(t, ts, http.MethodPost, base, `{"cluster":{"name":"gke1","initialNodeCount":1}}`); code != http.StatusOK {
		t.Fatalf("GKE create: code=%d body=%s", code, body)
	}

	// With no Kafka clusters in the location, the list is GKE's.
	if code, body := do(t, ts, http.MethodGet, base, ""); code != http.StatusOK || !strings.Contains(body, "gke1") {
		t.Fatalf("GKE list before Kafka: code=%d body=%s", code, body)
	}

	kafkaBody := `{"capacityConfig":{"vcpuCount":"3","memoryBytes":"3221225472"},` +
		`"gcpConfig":{"accessConfig":{"networkConfigs":[{"subnet":"projects/demo/regions/us-central1/subnetworks/s"}]}}}`

	code, createBody := do(t, ts, http.MethodPost, base+"?clusterId=kafka1", kafkaBody)
	if code != http.StatusOK || !strings.Contains(createBody, "google.cloud.managedkafka.v1.Cluster") {
		t.Fatalf("Kafka create: code=%d body=%s", code, createBody)
	}

	op := "/v1/" + opName(t, createBody)
	if code, body := do(t, ts, http.MethodGet, op, ""); code != http.StatusOK ||
		!strings.Contains(body, `"done":true`) || !strings.Contains(body, "managedkafka.v1.Cluster") {
		t.Fatalf("Kafka op GET via shared poller: code=%d body=%s", code, body)
	}

	// Each item is served by its owner.
	if code, body := do(t, ts, http.MethodGet, base+"/kafka1", ""); code != http.StatusOK || !strings.Contains(body, "capacityConfig") {
		t.Fatalf("Kafka get: code=%d body=%s", code, body)
	}

	if code, body := do(t, ts, http.MethodGet, base+"/gke1", ""); code != http.StatusOK || strings.Contains(body, "capacityConfig") {
		t.Fatalf("GKE get: code=%d body=%s", code, body)
	}

	// A bogus operation in the same location still 404s.
	if code, _ := do(t, ts, http.MethodGet, "/v1/projects/demo/locations/us-central1/operations/nope", ""); code != http.StatusNotFound {
		t.Fatalf("bogus op GET: code=%d, want 404", code)
	}

	// AFTER a Kafka create, the shared-location list is still GKE's: gke1 is
	// listed and the Kafka cluster does not replace it.
	if code, body := do(t, ts, http.MethodGet, base, ""); code != http.StatusOK ||
		!strings.Contains(body, "gke1") || strings.Contains(body, "kafka1") {
		t.Fatalf("GKE list after Kafka create: code=%d body=%s (want gke1, not kafka1)", code, body)
	}

	// In a location where GKE owns nothing, the list is Kafka's.
	const west = "/v1/projects/demo/locations/europe-west1/clusters"

	westBody := strings.ReplaceAll(kafkaBody, "us-central1", "europe-west1")
	if code, body := do(t, ts, http.MethodPost, west+"?clusterId=kafka2", westBody); code != http.StatusOK {
		t.Fatalf("Kafka create in europe-west1: code=%d body=%s", code, body)
	}

	if code, body := do(t, ts, http.MethodGet, west, ""); code != http.StatusOK || !strings.Contains(body, "clusters/kafka2") {
		t.Fatalf("Kafka list where GKE owns none: code=%d body=%s", code, body)
	}

	// A Kafka create reusing a GKE cluster's id in that location is refused, and
	// the GKE cluster stays reachable; so is a GKE create reusing a Kafka id.
	if code, body := do(t, ts, http.MethodPost, base+"?clusterId=gke1", kafkaBody); code != http.StatusConflict ||
		!strings.Contains(body, "ALREADY_EXISTS") {
		t.Fatalf("Kafka create over GKE id: code=%d body=%s (want 409 ALREADY_EXISTS)", code, body)
	}

	if code, body := do(t, ts, http.MethodPost, base, `{"cluster":{"name":"kafka1","initialNodeCount":1}}`); code != http.StatusConflict {
		t.Fatalf("GKE create over Kafka id: code=%d body=%s (want 409)", code, body)
	}

	if code, body := do(t, ts, http.MethodGet, base+"/gke1", ""); code != http.StatusOK || strings.Contains(body, "capacityConfig") {
		t.Fatalf("GKE get after refused Kafka create: code=%d body=%s", code, body)
	}

	// A Kafka-shaped PATCH for a Kafka cluster that no longer exists is Kafka's
	// 404, not GKE's 405.
	if code, body := do(t, ts, http.MethodDelete, base+"/kafka1", ""); code != http.StatusOK {
		t.Fatalf("Kafka delete: code=%d body=%s", code, body)
	}

	for _, body := range []string{`{"labels":{"a":"b"}}`, `{"capacityConfig":{"vcpuCount":"4"}}`} {
		code, got := do(t, ts, http.MethodPatch, base+"/kafka1?updateMask=labels", body)
		if code != http.StatusNotFound || !strings.Contains(got, "NOT_FOUND") {
			t.Fatalf("PATCH missing Kafka cluster %s: code=%d body=%s (want 404 NOT_FOUND)", body, code, got)
		}
	}
}

// TestFullServerManagedKafkaSharesClustersWithAlloyDB is the AlloyDB variant:
// AlloyDB's list survives a Kafka create, and neither service can take an id
// the other already uses.
func TestFullServerManagedKafkaSharesClustersWithAlloyDB(t *testing.T) {
	ts := httptest.NewServer(gcpserver.New(gcpserver.DriversFromWithAlloyDB(cloudemu.NewGCP())))
	t.Cleanup(ts.Close)

	const base = "/v1/projects/demo/locations/us-central1/clusters"

	if code, body := do(t, ts, http.MethodPost, base+"?clusterId=adb1", `{"network":"n"}`); code != http.StatusOK {
		t.Fatalf("AlloyDB create: code=%d body=%s", code, body)
	}

	kafkaBody := `{"capacityConfig":{"vcpuCount":"3","memoryBytes":"3221225472"},` +
		`"gcpConfig":{"accessConfig":{"networkConfigs":[{"subnet":"projects/demo/regions/us-central1/subnetworks/s"}]}}}`

	if code, body := do(t, ts, http.MethodPost, base+"?clusterId=kafka1", kafkaBody); code != http.StatusOK {
		t.Fatalf("Kafka create: code=%d body=%s", code, body)
	}

	if code, body := do(t, ts, http.MethodGet, base, ""); code != http.StatusOK ||
		!strings.Contains(body, "adb1") || strings.Contains(body, "kafka1") {
		t.Fatalf("AlloyDB list after Kafka create: code=%d body=%s (want adb1, not kafka1)", code, body)
	}

	if code, body := do(t, ts, http.MethodGet, base+"/kafka1", ""); code != http.StatusOK || !strings.Contains(body, "capacityConfig") {
		t.Fatalf("Kafka get: code=%d body=%s", code, body)
	}

	if code, body := do(t, ts, http.MethodGet, base+"/adb1", ""); code != http.StatusOK || strings.Contains(body, "capacityConfig") {
		t.Fatalf("AlloyDB get: code=%d body=%s", code, body)
	}

	if code, body := do(t, ts, http.MethodPost, base+"?clusterId=adb1", kafkaBody); code != http.StatusConflict {
		t.Fatalf("Kafka create over AlloyDB id: code=%d body=%s (want 409)", code, body)
	}

	if code, body := do(t, ts, http.MethodPost, base+"?clusterId=kafka1", `{"network":"n"}`); code != http.StatusConflict {
		t.Fatalf("AlloyDB create over Kafka id: code=%d body=%s (want 409)", code, body)
	}

	// An AlloyDB labels PATCH on its own cluster still reaches AlloyDB.
	if code, body := do(t, ts, http.MethodPatch, base+"/adb1?updateMask=labels", `{"labels":{"a":"b"}}`); code != http.StatusOK ||
		strings.Contains(body, "capacityConfig") {
		t.Fatalf("AlloyDB PATCH own cluster: code=%d body=%s", code, body)
	}
}

// TestFullServerBackupDROperationsResolveThroughSharedPoller proves a Backup and
// DR vault operation is recorded with the shared lro registry (not answered by
// a greedy sibling handler): the poll returns done with the typed BackupVault
// response, cancel/delete act on the real record, and a later poll 404s.
func TestFullServerBackupDROperationsResolveThroughSharedPoller(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost,
		"/v1/projects/demo/locations/us-central1/backupVaults?backupVaultId=vault-ops",
		`{"backupMinimumEnforcedRetentionDuration":"86400s"}`)
	if code != http.StatusOK {
		t.Fatalf("BackupDR create: code=%d body=%s", code, body)
	}

	op := "/v1/" + opName(t, body)

	code, body = do(t, ts, http.MethodGet, op, "")
	if code != http.StatusOK || !strings.Contains(body, `"done":true`) ||
		!strings.Contains(body, "google.cloud.backupdr.v1.BackupVault") {
		t.Fatalf("BackupDR op GET: code=%d body=%s (want 200 done with BackupVault response)", code, body)
	}

	if code, body := do(t, ts, http.MethodDelete, op, ""); code != http.StatusOK {
		t.Fatalf("BackupDR op delete: code=%d body=%s (want 200)", code, body)
	}

	if code, _ := do(t, ts, http.MethodGet, op, ""); code != http.StatusNotFound {
		t.Fatalf("BackupDR op GET after delete: code=%d (want 404)", code)
	}
}
