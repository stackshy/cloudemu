package gcp_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestVertexEndpointsNotShadowedByIDS (GVAI-01): Vertex AI endpoint traffic is
// no longer captured by Cloud IDS, and IDS creates still reach IDS.
func TestVertexEndpointsNotShadowedByIDS(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/endpoints?endpointId=111", `{"displayName":"ep1"}`)
	wantCode(t, "Vertex endpoint create", code, body, http.StatusOK, "aiplatform")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/endpoints/111:predict", `{"instances":[{"x":1}]}`)
	if code == http.StatusMethodNotAllowed || strings.Contains(body, "Cloud IDS") {
		t.Fatalf(":predict: code=%d body=%.300s, want a Vertex answer", code, body)
	}

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/endpoints", "")
	wantCode(t, "Vertex endpoint list", code, body, http.StatusOK, "ep1")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/endpoints?endpointId=ids1",
		`{"network":"projects/demo/global/networks/default","severity":"INFORMATIONAL"}`)
	wantCode(t, "IDS endpoint create", code, body, http.StatusOK, "google.cloud.ids.v1.Endpoint")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/endpoints/ids1", "")
	wantCode(t, "IDS endpoint get", code, body, http.StatusOK, "INFORMATIONAL")
}

// TestProjectScopedPublisherGenerateContent (GVAI-02): the project-scoped
// publisher model path the Vertex SDKs call is served, on v1 and v1beta1.
func TestProjectScopedPublisherGenerateContent(t *testing.T) {
	ts := fullServer(t)

	const req = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

	for _, ver := range []string{"v1", "v1beta1"} {
		path := "/" + ver + "/projects/demo/locations/us-central1/publishers/google/models/gemini-2.0-flash:generateContent"

		code, body := do(t, ts, http.MethodPost, path, req)
		wantCode(t, ver+" generateContent", code, body, http.StatusOK, "candidates")
	}
}

// TestSQLAdminV1ListNotSpanner (GSQL-06): with no Spanner instances, the shared
// v1 instance list is Cloud SQL's. A Spanner-paged list is Spanner's.
func TestSQLAdminV1ListNotSpanner(t *testing.T) {
	ts := fullServer(t)

	for _, name := range []string{"sql1", "sql2"} {
		code, body := do(t, ts, http.MethodPost, "/v1/projects/demo/instances",
			`{"name":"`+name+`","databaseVersion":"POSTGRES_15","settings":{"tier":"db-f1-micro"}}`)
		wantCode(t, "Cloud SQL create "+name, code, body, http.StatusOK, "")
	}

	code, body := do(t, ts, http.MethodGet, "/v1/projects/demo/instances", "")
	if code != http.StatusOK || !strings.Contains(body, "sql#instancesList") ||
		!strings.Contains(body, "sql1") || !strings.Contains(body, "sql2") {
		t.Fatalf("Cloud SQL v1 list: code=%d body=%.300s", code, body)
	}

	code, body = do(t, ts, http.MethodPost, "/v1/projects/demo/instances",
		`{"instanceId":"s1","instance":{"config":"regional-us-central1","displayName":"s1","nodeCount":1}}`)
	wantCode(t, "Spanner create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, "/v1/projects/demo/instances?pageSize=10", "")
	if code != http.StatusOK || !strings.Contains(body, "instances/s1") || strings.Contains(body, "sql#") {
		t.Fatalf("Spanner paged list: code=%d body=%.300s", code, body)
	}
}

// TestSpannerMissingInstance404 (GSPN-05): Spanner-only shapes on a missing
// instance get Spanner's 404, not a Cloud SQL error.
func TestSpannerMissingInstance404(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, "/v1/projects/demo/instances/nope/databases",
		`{"createStatement":"CREATE DATABASE d"}`)
	wantCode(t, "create database on missing instance", code, body, http.StatusNotFound, "NOT_FOUND")

	code, body = do(t, ts, http.MethodGet, "/v1/projects/demo/instances/nope/operations", "")
	wantCode(t, "operations on missing instance", code, body, http.StatusNotFound, "NOT_FOUND")
}

// TestFilestoreUnknownTierIsFilestore400 (GFST-01): an invalid Filestore tier
// is Filestore's 400 instead of a READY Redis instance.
func TestFilestoreUnknownTierIsFilestore400(t *testing.T) {
	ts := fullServer(t)

	const zone = "/v1/projects/demo/locations/us-central1-a/instances"

	code, body := do(t, ts, http.MethodPost, zone+"?instanceId=f3", `{"tier":"NOPE"}`)
	wantCode(t, "bad tier create", code, body, http.StatusBadRequest, "")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/instances?instanceId=f4", `{"tier":"NOPE"}`)
	wantCode(t, "bad tier create, regional", code, body, http.StatusBadRequest, "")

	code, body = do(t, ts, http.MethodGet, "/redis.googleapis.com"+w1aLoc+"/instances", "")
	if code != http.StatusOK || strings.Contains(body, "f3") || strings.Contains(body, "f4") {
		t.Fatalf("Redis list: code=%d body=%.300s, want no f3/f4", code, body)
	}
}

// TestBackupDRPlanNotCapturedByGKEBackup (GBDR-02, routing half): a Backup and
// DR plan create gets Backup and DR's 501, and Backup for GKE stores nothing.
func TestBackupDRPlanNotCapturedByGKEBackup(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/backupPlans?backupPlanId=bp",
		`{"backupVault":"projects/demo/locations/us-central1/backupVaults/v","resourceType":"compute.googleapis.com/Instance","backupRules":[{"ruleId":"r"}]}`)
	wantCode(t, "Backup and DR plan create", code, body, http.StatusNotImplemented, "Backup and DR")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/backupPlans?backupPlanId=bp",
		`{"cluster":"projects/demo/locations/us-central1/clusters/c1"}`)
	wantCode(t, "GKE backup plan create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/managementServers", "")
	wantCode(t, "managementServers", code, body, http.StatusNotImplemented, "")
}

// TestDataformV1Repositories (GDFM-01): Dataform v1 repositories reach Dataform
// and Artifact Registry keeps its own.
func TestDataformV1Repositories(t *testing.T) {
	ts := fullServer(t)

	code, body := do(t, ts, http.MethodPost, w1aLoc+"/repositories?repositoryId=r1",
		`{"gitRemoteSettings":{"url":"https://example.com/r.git","defaultBranch":"main"}}`)
	wantCode(t, "Dataform v1 create", code, body, http.StatusOK, "gitRemoteSettings")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/repositories/r1", "")
	wantCode(t, "Dataform v1 get", code, body, http.StatusOK, "gitRemoteSettings")

	code, body = do(t, ts, http.MethodPost, w1aLoc+"/repositories?repositoryId=ar1", `{"format":"DOCKER"}`)
	wantCode(t, "Artifact Registry create", code, body, http.StatusOK, "")

	code, body = do(t, ts, http.MethodGet, w1aLoc+"/repositories/ar1", "")
	wantCode(t, "Artifact Registry get", code, body, http.StatusOK, "DOCKER")
}
