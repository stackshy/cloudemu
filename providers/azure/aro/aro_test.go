package aro_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/aro"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const (
	testSub = "sub-1"
	testRG  = "rg-1"
)

// serverURLFromKubeconfig extracts the cluster server URL from a rendered
// kubeconfig.
func serverURLFromKubeconfig(t *testing.T, kubeconfig []byte) string {
	t.Helper()

	for _, line := range strings.Split(string(kubeconfig), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "server:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "server:"))
		}
	}

	t.Fatalf("no server URL in kubeconfig:\n%s", kubeconfig)

	return ""
}

// TestARO_CreateGetDelete_WithDataPlane is the end-to-end provisioning story:
// creating an ARO cluster backs it with a real OpenShift data plane whose
// kubeconfig reaches the OpenShift API; deleting it tears the data plane down.
func TestARO_CreateGetDelete_WithDataPlane(t *testing.T) {
	api := kubernetes.NewAPIServer()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	api.SetBaseURL(ts.URL)

	m := aro.New(config.NewOptions())
	m.SetK8sAPI(api)

	ctx := context.Background()

	cluster, err := m.CreateOrUpdateCluster(ctx, aro.ClusterInput{
		Subscription: testSub, ResourceGroup: testRG, Name: "ocp1", Location: "eastus",
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateCluster: %v", err)
	}

	if cluster.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState: got %q, want Succeeded", cluster.ProvisioningState)
	}

	if cluster.Version != "4.16.0" {
		t.Errorf("version: got %q, want 4.16.0", cluster.Version)
	}

	// The kubeconfig must reach a real OpenShift-flavored data plane: hitting the
	// ClusterVersion singleton through it proves the cluster was registered with
	// FlavorOpenShift (a plain Kubernetes cluster would 404 that path).
	server := serverURLFromKubeconfig(t, m.Kubeconfig(testSub, testRG, "ocp1"))
	if !strings.Contains(server, "/k8s/") {
		t.Fatalf("kubeconfig server %q does not point at the data plane", server)
	}

	resp, err := http.Get(server + "/apis/config.openshift.io/v1/clusterversions/version") //nolint:noctx // test.
	if err != nil {
		t.Fatalf("GET clusterversion via kubeconfig: %v", err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clusterversion via ARO kubeconfig: status %d, want 200 (OpenShift data plane not provisioned)",
			resp.StatusCode)
	}

	// Get returns the stored cluster.
	got, err := m.GetCluster(ctx, testSub, testRG, "ocp1")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}

	if got.ID == "" || !strings.Contains(got.ID, "Microsoft.RedHatOpenShift") {
		t.Errorf("cluster ID malformed: %q", got.ID)
	}

	// Delete tears down the data plane.
	if err := m.DeleteCluster(ctx, testSub, testRG, "ocp1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := m.GetCluster(ctx, testSub, testRG, "ocp1"); err == nil {
		t.Error("GetCluster after delete: want error, got nil")
	}

	after, err := http.Get(server + "/apis/config.openshift.io/v1/clusterversions/version") //nolint:noctx // test.
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}

	after.Body.Close()

	if after.StatusCode != http.StatusNotFound {
		t.Errorf("data plane after delete: status %d, want 404 (deregistered)", after.StatusCode)
	}
}

// TestARO_ListAndFallbackKubeconfig covers listing and the no-data-plane
// kubeconfig fallback.
func TestARO_ListAndFallbackKubeconfig(t *testing.T) {
	m := aro.New(config.NewOptions()) // no k8sAPI wired
	ctx := context.Background()

	for _, name := range []string{"a", "b"} {
		if _, err := m.CreateOrUpdateCluster(ctx, aro.ClusterInput{
			Subscription: testSub, ResourceGroup: testRG, Name: name, Location: "eastus",
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if got := m.ListClustersByResourceGroup(ctx, testSub, testRG); len(got) != 2 {
		t.Fatalf("list by rg: got %d, want 2", len(got))
	}

	// No data plane wired -> sentinel kubeconfig, still structurally valid.
	kc := string(m.Kubeconfig(testSub, testRG, "a"))
	if !strings.Contains(kc, "ARO-DATAPLANE-NOT-IMPLEMENTED") {
		t.Errorf("fallback kubeconfig missing sentinel:\n%s", kc)
	}
}
