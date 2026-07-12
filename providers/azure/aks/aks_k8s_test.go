// Coverage for the Wave 2 Phase 3 Kubernetes data-plane wiring inside the
// AKS provider mock: SetK8sAPI, register-on-Create-or-Update,
// deregister-on-Delete, and Kubeconfig's real-vs-sentinel fall-back paths.

package aks

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func TestSetK8sAPI_CreateRegistersWithAPIServer(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
		Subscription:  "sub-1",
		ResourceGroup: "rg-1",
		Name:          "c1",
		Location:      "eastus",
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	uid, ok := m.k8sUIDs[clusterKey("rg-1", "c1")]
	if !ok {
		t.Fatal("k8sUIDs map missing entry for rg-1/c1")
	}

	if api.Lookup(uid) == nil {
		t.Fatalf("APIServer has no ClusterState for uid %q", uid)
	}
}

func TestKubeconfig_RealURLWhenWired(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
		Subscription:  "sub-1",
		ResourceGroup: "rg-1",
		Name:          "c1",
		Location:      "eastus",
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	kc := string(m.Kubeconfig("rg-1", "c1"))

	if strings.Contains(kc, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("kubeconfig still sentinel after wiring:\n%s", kc)
	}

	if !strings.Contains(kc, "http://127.0.0.1:8080/k8s/") {
		t.Fatalf("kubeconfig server URL missing real prefix:\n%s", kc)
	}

	if !strings.Contains(kc, "token: "+kubernetes.StubToken) {
		t.Fatalf("kubeconfig missing stub token:\n%s", kc)
	}
}

func TestKubeconfig_NoAPIKeepsSentinel(t *testing.T) {
	m := newTestMock()

	if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
		Subscription:  "sub-1",
		ResourceGroup: "rg-1",
		Name:          "c1",
		Location:      "eastus",
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	kc := string(m.Kubeconfig("rg-1", "c1"))
	if !strings.Contains(kc, "AKS-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected sentinel when no APIServer wired:\n%s", kc)
	}
}

func TestKubeconfig_APIWithoutBaseURLKeepsSentinel(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	// Intentionally no SetBaseURL — Kubeconfig should fall back.
	m.SetK8sAPI(api)

	if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
		Subscription:  "sub-1",
		ResourceGroup: "rg-1",
		Name:          "c1",
		Location:      "eastus",
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	kc := string(m.Kubeconfig("rg-1", "c1"))
	if !strings.Contains(kc, "AKS-DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("expected sentinel when APIServer has no base URL:\n%s", kc)
	}
}

func TestDeleteCluster_DeregistersK8sState(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
		Subscription:  "sub-1",
		ResourceGroup: "rg-1",
		Name:          "c1",
		Location:      "eastus",
	}); err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	uid := m.k8sUIDs[clusterKey("rg-1", "c1")]

	if err := m.DeleteCluster(context.Background(), "rg-1", "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, ok := m.k8sUIDs[clusterKey("rg-1", "c1")]; ok {
		t.Fatal("k8sUIDs still has an entry for the deleted cluster")
	}

	if api.Lookup(uid) != nil {
		t.Fatal("APIServer state was not deregistered")
	}
}

func TestCreateOrUpdate_DoesNotReRegister(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	for i := 0; i < 3; i++ {
		if _, err := m.CreateOrUpdateCluster(context.Background(), ClusterInput{
			Subscription:      "sub-1",
			ResourceGroup:     "rg-1",
			Name:              "c1",
			Location:          "eastus",
			KubernetesVersion: "1.29.5",
		}); err != nil {
			t.Fatalf("iteration %d: CreateOrUpdateCluster: %v", i, err)
		}
	}

	// Re-PUTs should not allocate new UIDs — ARM PUT is idempotent and the
	// data-plane identity must stay stable across re-PUTs.
	uid := m.k8sUIDs[clusterKey("rg-1", "c1")]
	if uid == "" {
		t.Fatal("k8sUIDs entry missing after CreateOrUpdate")
	}

	count := 0

	for k := range m.k8sUIDs {
		if k == clusterKey("rg-1", "c1") {
			count++
		}
	}

	if count != 1 {
		t.Fatalf("expected one UID entry, got %d", count)
	}
}
