// Coverage for the Wave 2 Kubernetes data-plane wiring inside the EKS
// provider mock: SetK8sAPI, register-on-Create, deregister-on-Delete, and
// withK8sEndpoint's endpoint rewriting (including its fall-back cases).

package eks

import (
	"context"
	"strings"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func TestSetK8sAPI_CreateClusterRegistersWithAPIServer(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	_, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name:    "c1",
		Version: "1.30",
		RoleArn: "arn:aws:iam::123456789012:role/eks",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	uid, ok := m.k8sUIDs["c1"]
	if !ok {
		t.Fatal("k8sUIDs map missing entry for c1")
	}

	if api.Lookup(uid) == nil {
		t.Fatalf("APIServer has no ClusterState for uid %q", uid)
	}
}

func TestDescribeCluster_EndpointRewrittenToK8s(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	_, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name:    "c1",
		Version: "1.30",
		RoleArn: "arn:aws:iam::123456789012:role/eks",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeCluster(context.Background(), "c1")
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if !strings.HasPrefix(got.Endpoint, "http://127.0.0.1:8080/k8s/") {
		t.Fatalf("Endpoint not rewritten: %q", got.Endpoint)
	}

	if strings.Contains(got.Endpoint, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("Endpoint still the sentinel: %q", got.Endpoint)
	}
}

func TestDescribeCluster_NoAPIKeepsSentinel(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name:    "c1",
		Version: "1.30",
		RoleArn: "arn:aws:iam::123456789012:role/eks",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeCluster(context.Background(), "c1")
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if !strings.Contains(got.Endpoint, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("with no APIServer set Endpoint should be the sentinel, got %q", got.Endpoint)
	}
}

func TestDescribeCluster_APIWithoutBaseURLKeepsSentinel(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	// Intentionally no SetBaseURL — withK8sEndpoint should fall back.
	m.SetK8sAPI(api)

	_, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name:    "c1",
		Version: "1.30",
		RoleArn: "arn:aws:iam::123456789012:role/eks",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeCluster(context.Background(), "c1")
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if !strings.Contains(got.Endpoint, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("Endpoint should stay sentinel when API has no BaseURL, got %q", got.Endpoint)
	}
}

func TestDeleteCluster_DeregistersK8sState(t *testing.T) {
	m := newTestMock()

	api := kubernetes.NewAPIServer()
	api.SetBaseURL("http://127.0.0.1:8080")
	m.SetK8sAPI(api)

	_, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name:    "c1",
		Version: "1.30",
		RoleArn: "arn:aws:iam::123456789012:role/eks",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	uid := m.k8sUIDs["c1"]

	if _, err := m.DeleteCluster(context.Background(), "c1"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, ok := m.k8sUIDs["c1"]; ok {
		t.Fatal("k8sUIDs still has an entry for the deleted cluster")
	}

	if api.Lookup(uid) != nil {
		t.Fatal("APIServer state was not deregistered")
	}
}
