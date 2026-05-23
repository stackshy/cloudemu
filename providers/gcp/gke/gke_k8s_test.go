// Coverage for the Wave 2 Phase 3 Kubernetes data-plane wiring inside the
// GKE provider mock: SetK8sAPI, register-on-Create,
// deregister-on-Delete, and Endpoint's real-vs-sentinel fall-back paths.

package gke

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/kubernetes"
)

func TestSetK8sAPI_CreateRegistersWithAPIServer(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	if _, _, err := m.CreateCluster(context.Background(), &CreateClusterInput{
		Name:             "c1",
		Location:         "us-central1",
		InitialNodeCount: 1,
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	uid, ok := m.k8sUIDs[clusterKey("us-central1", "c1")]
	if !ok {
		t.Fatal("k8sUIDs map missing entry for us-central1/c1")
	}

	if api.Lookup(uid) == nil {
		t.Fatalf("APIServer has no ClusterState for uid %q", uid)
	}
}

func TestEndpoint_RealURLWhenWired(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	if _, _, err := m.CreateCluster(context.Background(), &CreateClusterInput{
		Name:             "c1",
		Location:         "us-central1",
		InitialNodeCount: 1,
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got := m.Endpoint("us-central1", "c1")

	if strings.Contains(got, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("Endpoint still sentinel after wiring: %q", got)
	}

	if !strings.HasPrefix(got, "http://127.0.0.1:8080/k8s/") {
		t.Fatalf("Endpoint missing real prefix: %q", got)
	}
}

func TestEndpoint_NoAPIKeepsSentinel(t *testing.T) {
	m := newTestMock()

	if _, _, err := m.CreateCluster(context.Background(), &CreateClusterInput{
		Name:             "c1",
		Location:         "us-central1",
		InitialNodeCount: 1,
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got := m.Endpoint("us-central1", "c1")
	if !strings.Contains(got, "GKE-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected sentinel when no APIServer wired, got: %q", got)
	}
}

func TestEndpoint_APIWithoutBaseURLKeepsSentinel(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	// Intentionally no SetBaseURL — Endpoint should fall back.
	m.SetK8sAPI(api)

	if _, _, err := m.CreateCluster(context.Background(), &CreateClusterInput{
		Name:             "c1",
		Location:         "us-central1",
		InitialNodeCount: 1,
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got := m.Endpoint("us-central1", "c1")
	if !strings.Contains(got, "GKE-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected sentinel when APIServer has no base URL, got: %q", got)
	}
}

func TestEndpoint_UnknownClusterFallsBackToSentinel(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	// Cluster never created — Endpoint should still return the sentinel
	// rather than a half-built URL.
	got := m.Endpoint("us-central1", "ghost")
	if !strings.Contains(got, "GKE-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected sentinel for unknown cluster, got: %q", got)
	}
}

func TestDeleteCluster_DeregistersK8sState(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	if _, _, err := m.CreateCluster(context.Background(), &CreateClusterInput{
		Name:             "c1",
		Location:         "us-central1",
		InitialNodeCount: 1,
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	uid := m.k8sUIDs[clusterKey("us-central1", "c1")]

	if _, err := m.DeleteCluster(context.Background(), "us-central1", "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, ok := m.k8sUIDs[clusterKey("us-central1", "c1")]; ok {
		t.Fatal("k8sUIDs still has an entry for the deleted cluster")
	}

	if api.Lookup(uid) != nil {
		t.Fatal("APIServer state was not deregistered")
	}
}
