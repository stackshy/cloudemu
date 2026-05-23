// Wave 2 Phase 3 end-to-end test: real armcontainerservice creates an AKS
// cluster, ListClusterAdminCredentials returns a kubeconfig pointing at the
// in-memory K8s API server, and real client-go drives a workload stack
// against the resulting endpoint.

package aks_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/stackshy/cloudemu"
	cloudkube "github.com/stackshy/cloudemu/kubernetes"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// TestSDKAKSDataPlane_FullWorkloadStack drives the end-to-end path:
//   - real armcontainerservice creates a managed cluster against cloudemu.
//   - ListClusterAdminCredentials returns a kubeconfig that points at the
//     in-memory Kubernetes API server (not the *-DATAPLANE-NOT-IMPLEMENTED
//     sentinel) when SetK8sAPI is wired.
//   - clientcmd.RESTConfigFromKubeConfig parses the kubeconfig, and the
//     resulting rest.Config is used to drive a full Phase-2 workload stack
//     (Namespace + ServiceAccount + Secret + ConfigMap + Deployment +
//     Service + Pod) via real client-go.
//   - Deleting the cluster tears the K8s state down — subsequent client-go
//     calls against the orphaned endpoint fail.
//
//nolint:funlen // single end-to-end scenario across many resource kinds.
func TestSDKAKSDataPlane_FullWorkloadStack(t *testing.T) {
	cloudP := cloudemu.NewAzure()

	k8sAPI := cloudkube.NewAPIServer()
	cloudP.AKS.SetK8sAPI(k8sAPI)

	srv := azureserver.New(azureserver.Drivers{
		AKS:    cloudP.AKS,
		K8sAPI: k8sAPI,
	})
	ts := httptest.NewTLSServer(srv)

	t.Cleanup(ts.Close)

	k8sAPI.SetBaseURL(ts.URL)

	clusters := newAKSClusterClient(t, ts)

	ctx := context.Background()
	const rg, name = "rg-wave2", "shop-cluster"

	poller, err := clusters.BeginCreateOrUpdate(ctx, rg, name, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	resp, err := clusters.ListClusterAdminCredentials(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("ListClusterAdminCredentials: %v", err)
	}

	if len(resp.CredentialResults.Kubeconfigs) != 1 {
		t.Fatalf("got %d kubeconfigs, want 1", len(resp.CredentialResults.Kubeconfigs))
	}

	kubeconfig := resp.CredentialResults.Kubeconfigs[0].Value
	if strings.Contains(string(kubeconfig), "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("Wave 2 regression: kubeconfig still points at the Wave-1 sentinel:\n%s", string(kubeconfig))
	}

	if !strings.Contains(string(kubeconfig), ts.URL+"/k8s/") {
		t.Fatalf("kubeconfig server URL missing %s/k8s/ prefix:\n%s", ts.URL, string(kubeconfig))
	}

	cs := mustClientsetFromKubeconfig(t, kubeconfig)

	// Drive a Phase-2 workload stack — same surface the EKS test exercises.
	if _, err := cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}},
		metav1.CreateOptions{}); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	// default SA auto-created in every namespace.
	if _, err := cs.CoreV1().ServiceAccounts("shop").Get(ctx, "default", metav1.GetOptions{}); err != nil {
		t.Fatalf("default SA in shop: %v", err)
	}

	sec, err := cs.CoreV1().Secrets("shop").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds"},
		StringData: map[string]string{"user": "admin"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if string(sec.Data["user"]) != "admin" {
		t.Fatalf("StringData merge: data.user = %q", string(sec.Data["user"]))
	}

	if _, err := cs.CoreV1().ConfigMaps("shop").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "config"},
		Data:       map[string]string{"log_level": "info"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("CreateConfigMap: %v", err)
	}

	var replicas int32 = 3

	dep, err := cs.AppsV1().Deployments("shop").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "nginx", Image: "nginx:1.27"}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if dep.Status.ReadyReplicas != 3 {
		t.Fatalf("Deployment status not mirrored: ready=%d, want 3", dep.Status.ReadyReplicas)
	}

	svc, err := cs.CoreV1().Services("shop").Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if !strings.HasPrefix(svc.Spec.ClusterIP, "10.96.") {
		t.Fatalf("ClusterIP: got %q, want 10.96.x.x", svc.Spec.ClusterIP)
	}

	pod, err := cs.CoreV1().Pods("shop").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "side"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	if pod.Status.Phase != corev1.PodPending {
		t.Fatalf("Pod phase: got %q, want Pending", pod.Status.Phase)
	}

	// Delete the cluster — the K8s state must go with it.
	dPoller, err := clusters.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := dPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	// client-go against the now-orphaned endpoint should fail.
	if _, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		t.Fatal("client-go after DeleteCluster: expected error, got nil")
	}
}

// newAKSClusterClient builds an armcontainerservice ManagedClusters client
// pointing at the local httptest server, mirroring newSDKClients but with
// only the cluster-level client (AgentPools and MaintenanceConfigurations
// aren't needed for the data-plane round-trip).
func newAKSClusterClient(t *testing.T, ts *httptest.Server) *armcontainerservice.ManagedClustersClient {
	t.Helper()

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{}
	opts.Cloud = myCloud
	opts.Transport = ts.Client()

	c, err := armcontainerservice.NewManagedClustersClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewManagedClustersClient: %v", err)
	}

	return c
}

// mustClientsetFromKubeconfig parses kubeconfig YAML and returns a Clientset
// ready to drive the in-memory K8s server. The kubeconfig embeds
// insecure-skip-tls-verify so the httptest self-signed cert doesn't trip
// client-go's TLS validation.
func mustClientsetFromKubeconfig(t *testing.T, kubeconfig []byte) *kubernetes.Clientset {
	t.Helper()

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		t.Fatalf("RESTConfigFromKubeConfig: %v", err)
	}

	cfg.ContentType = "application/json"
	cfg.AcceptContentTypes = "application/json"
	cfg.GroupVersion = &corev1.SchemeGroupVersion
	cfg.NegotiatedSerializer = kubescheme.Codecs.WithoutConversion()

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}

	return cs
}
