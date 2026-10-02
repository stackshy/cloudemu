package aks

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

func TestK8sVersion_FollowsKubernetesVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	in := ClusterInput{
		Subscription: "sub-1", ResourceGroup: "rg-1", Name: "c1", Location: "eastus",
		KubernetesVersion: "1.31.2",
	}
	if _, err := m.CreateOrUpdateCluster(ctx, in); err != nil {
		t.Fatalf("create: %v", err)
	}

	uid := m.k8sUIDs[clusterKey("rg-1", "c1")]

	if git, minor := dataPlaneVersion(t, api, uid); git != "v1.31.2" || minor != "31" {
		t.Fatalf("after create: gitVersion %q minor %q, want v1.31.2 / 31", git, minor)
	}

	in.KubernetesVersion = "1.32.1"
	if _, err := m.CreateOrUpdateCluster(ctx, in); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	if git, minor := dataPlaneVersion(t, api, uid); git != "v1.32.1" || minor != "32" {
		t.Fatalf("after upgrade: gitVersion %q minor %q, want v1.32.1 / 32", git, minor)
	}

	// A PUT that omits kubernetesVersion keeps the stored one, and so /version.
	in.KubernetesVersion = ""
	if _, err := m.CreateOrUpdateCluster(ctx, in); err != nil {
		t.Fatalf("tags-only PUT: %v", err)
	}

	if git, _ := dataPlaneVersion(t, api, uid); git != "v1.32.1" {
		t.Fatalf("after version-less PUT: gitVersion %q, want v1.32.1", git)
	}
}

func TestK8sVersion_DefaultsToAKSDefault(t *testing.T) {
	m := newTestMock()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
		Subscription: "sub-1", ResourceGroup: "rg-1", Name: "c1", Location: "eastus",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if git, _ := dataPlaneVersion(t, api, m.k8sUIDs[clusterKey("rg-1", "c1")]); git != "v"+defaultK8sVersion {
		t.Fatalf("gitVersion %q, want v%s", git, defaultK8sVersion)
	}
}
