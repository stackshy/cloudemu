// Wave 2 Phase 3 end-to-end test: real container/v1 creates a GKE cluster
// against cloudemu, the cluster's Endpoint now points at the in-memory
// Kubernetes API server, and real client-go drives a workload stack
// through it.

package gke_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/stackshy/cloudemu"
	cloudkube "github.com/stackshy/cloudemu/kubernetes"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

// TestSDKGKEDataPlane_FullWorkloadStack drives the full path:
//   - real container/v1 (the SDK) creates a cluster.
//   - clusters.Get returns a Cluster whose Endpoint points at the in-memory
//     K8s API server (not the GKE-DATAPLANE-NOT-IMPLEMENTED sentinel).
//   - client-go uses that Endpoint to deploy a Phase-2 workload stack.
//   - clusters.Delete tears the K8s state down — subsequent client-go calls
//     against the orphaned endpoint fail.
//
//nolint:funlen // single end-to-end scenario across many resource kinds.
func TestSDKGKEDataPlane_FullWorkloadStack(t *testing.T) {
	cloud := cloudemu.NewGCP()

	k8sAPI := cloudkube.NewAPIServer()
	cloud.GKE.SetK8sAPI(k8sAPI)

	srv := gcpserver.New(gcpserver.Drivers{
		GKE:    cloud.GKE,
		K8sAPI: k8sAPI,
	})
	ts := httptest.NewServer(srv)

	t.Cleanup(ts.Close)

	k8sAPI.SetBaseURL(ts.URL)

	svc, err := container.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("container.NewService: %v", err)
	}

	const project, location, name = "mock-project", "us-central1", "shop"

	ctx := context.Background()
	parent := "projects/" + project + "/locations/" + location

	if _, err := svc.Projects.Locations.Clusters.Create(parent, &container.CreateClusterRequest{
		Cluster: &container.Cluster{
			Name:             name,
			InitialNodeCount: 1,
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(parent + "/clusters/" + name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	if strings.Contains(got.Endpoint, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("Wave 2 regression: Endpoint still points at the Wave-1 sentinel: %s", got.Endpoint)
	}

	if !strings.HasPrefix(got.Endpoint, ts.URL+"/k8s/") {
		t.Fatalf("Endpoint should start with %s/k8s/, got %q", ts.URL, got.Endpoint)
	}

	cs := mustClientset(t, got.Endpoint)

	// Drive a Phase-2 workload stack — same surface the EKS and AKS tests
	// exercise.
	if _, err := cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}},
		metav1.CreateOptions{}); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

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

	svcOut, err := cs.CoreV1().Services("shop").Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if !strings.HasPrefix(svcOut.Spec.ClusterIP, "10.96.") {
		t.Fatalf("ClusterIP: got %q, want 10.96.x.x", svcOut.Spec.ClusterIP)
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
	if _, err := svc.Projects.Locations.Clusters.Delete(parent + "/clusters/" + name).Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Delete: %v", err)
	}

	// client-go against the now-orphaned endpoint should fail.
	if _, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		t.Fatal("client-go after Clusters.Delete: expected error, got nil")
	}
}

// mustClientset builds a client-go Clientset talking plain HTTP to host with
// the kubernetes.StubToken bearer, mirroring what clientcmd would build from
// a kubeconfig that has insecure-skip-tls-verify=true and a static token.
func mustClientset(t *testing.T, host string) *kubernetes.Clientset {
	t.Helper()

	cs, err := kubernetes.NewForConfig(&rest.Config{
		Host:        host,
		BearerToken: cloudkube.StubToken,
		ContentConfig: rest.ContentConfig{
			ContentType:          "application/json",
			AcceptContentTypes:   "application/json",
			GroupVersion:         &corev1.SchemeGroupVersion,
			NegotiatedSerializer: kubescheme.Codecs.WithoutConversion(),
		},
	})
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}

	return cs
}
