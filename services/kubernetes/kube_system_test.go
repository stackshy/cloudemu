package kubernetes_test

import (
	"net/http"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// A fresh cluster's kube-system should look like a managed cluster: a 2-replica
// coredns Deployment (2 Running pods), a kube-proxy DaemonSet (1 pod on the sole
// node), and a kube-dns Service pinned to 10.96.0.10.

func TestKubeSystem_CoreDNSDeploymentRunning(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/kube-system/deployments/coredns", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get coredns: status %d", resp.StatusCode)
	}

	var dep appsv1.Deployment
	mustDecode(t, resp.Body, &dep)

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 2 {
		t.Fatalf("coredns spec.replicas: got %v, want 2", dep.Spec.Replicas)
	}

	if dep.Status.ReadyReplicas != 2 {
		t.Fatalf("coredns status.readyReplicas: got %d, want 2", dep.Status.ReadyReplicas)
	}

	pods := listKubeSystemPods(t, base, "k8s-app=kube-dns")
	if len(pods.Items) != 2 {
		t.Fatalf("coredns pods: got %d, want 2", len(pods.Items))
	}

	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodRunning {
			t.Fatalf("coredns pod %s: phase %q, want Running", p.Name, p.Status.Phase)
		}
	}
}

func TestKubeSystem_KubeProxyDaemonSetRunning(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/kube-system/daemonsets/kube-proxy", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get kube-proxy: status %d", resp.StatusCode)
	}

	var ds appsv1.DaemonSet
	mustDecode(t, resp.Body, &ds)

	if ds.Status.DesiredNumberScheduled != 1 || ds.Status.NumberReady != 1 {
		t.Fatalf("kube-proxy status: desired=%d ready=%d, want 1/1",
			ds.Status.DesiredNumberScheduled, ds.Status.NumberReady)
	}

	pods := listKubeSystemPods(t, base, "k8s-app=kube-proxy")
	if len(pods.Items) != 1 {
		t.Fatalf("kube-proxy pods: got %d, want 1", len(pods.Items))
	}

	if pods.Items[0].Status.Phase != corev1.PodRunning {
		t.Fatalf("kube-proxy pod phase: %q, want Running", pods.Items[0].Status.Phase)
	}
}

func TestKubeSystem_KubeDNSServicePinnedClusterIP(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/kube-system/services/kube-dns", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get kube-dns: status %d", resp.StatusCode)
	}

	var svc corev1.Service
	mustDecode(t, resp.Body, &svc)

	if svc.Spec.ClusterIP != "10.96.0.10" {
		t.Fatalf("kube-dns ClusterIP: got %q, want 10.96.0.10", svc.Spec.ClusterIP)
	}

	wantPorts := map[string]corev1.Protocol{
		"53/UDP": corev1.ProtocolUDP, "53/TCP": corev1.ProtocolTCP, "9153/TCP": corev1.ProtocolTCP,
	}
	if len(svc.Spec.Ports) != len(wantPorts) {
		t.Fatalf("kube-dns ports: got %d, want %d", len(svc.Spec.Ports), len(wantPorts))
	}

	// Its endpoints should be populated from the coredns pods.
	epResp := do(t, http.MethodGet, base+"/api/v1/namespaces/kube-system/endpoints/kube-dns", nil)
	var ep corev1.Endpoints
	mustDecode(t, epResp.Body, &ep)

	addrs := 0
	for _, s := range ep.Subsets {
		addrs += len(s.Addresses)
	}

	if addrs != 2 {
		t.Fatalf("kube-dns endpoints addresses: got %d, want 2 (coredns pods)", addrs)
	}
}

func listKubeSystemPods(t *testing.T, base, selector string) corev1.PodList {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/kube-system/pods?labelSelector="+selector, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list kube-system pods (%s): status %d", selector, resp.StatusCode)
	}

	var list corev1.PodList
	mustDecode(t, resp.Body, &list)

	return list
}
