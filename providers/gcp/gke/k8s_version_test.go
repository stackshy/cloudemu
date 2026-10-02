package gke

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func dataPlaneVersion(t *testing.T, api *kubernetes.APIServer, uid string) (gitVersion, minor string) {
	t.Helper()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/k8s/"+uid+"/version", http.NoBody))

	var body struct {
		Minor      string `json:"minor"`
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /version: %v (%s)", err, rec.Body.String())
	}

	return body.GitVersion, body.Minor
}

func TestK8sVersion_FollowsMasterVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	if _, _, err := m.CreateCluster(ctx, &CreateClusterInput{
		Name: "c1", Location: "us-central1", InitialNodeCount: int64Ptr(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	uid := m.k8sUIDs[clusterKey("us-central1", "c1")]

	if git, minor := dataPlaneVersion(t, api, uid); git != "v"+StubMasterVer || minor != "30+" {
		t.Fatalf("after create: gitVersion %q minor %q, want v%s / 30+", git, minor, StubMasterVer)
	}

	if _, err := m.UpdateCluster(ctx, "us-central1", "c1", UpdateClusterInput{MasterVersion: "1.31.4-gke.1256000"}); err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	if git, minor := dataPlaneVersion(t, api, uid); git != "v1.31.4-gke.1256000" || minor != "31+" {
		t.Fatalf("after upgrade: gitVersion %q minor %q, want v1.31.4-gke.1256000 / 31+", git, minor)
	}

	// An update that leaves the master alone keeps /version.
	if _, err := m.UpdateCluster(ctx, "us-central1", "c1", UpdateClusterInput{LoggingService: "none"}); err != nil {
		t.Fatalf("UpdateCluster logging: %v", err)
	}

	if git, _ := dataPlaneVersion(t, api, uid); git != "v1.31.4-gke.1256000" {
		t.Fatalf("after unrelated update: gitVersion %q", git)
	}
}
