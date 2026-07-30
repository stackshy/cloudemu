package gke_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/k8spki"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	cloudkube "github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestSDKGKEDataPlane_TLSValidatesAdvertisedCA is the connect-parity proof for
// GKE: the data plane is served over HTTPS with the shared k8spki certificate,
// and client-go validates the endpoint against the CA the cluster advertises in
// masterAuth.clusterCaCertificate — no skip-verify. Before the CA fix GKE
// advertised an unparseable dummy blob and this handshake failed.
func TestSDKGKEDataPlane_TLSValidatesAdvertisedCA(t *testing.T) {
	cloud := cloudemu.NewGCP()

	k8sAPI := cloudkube.NewAPIServer()
	cloud.GKE.SetK8sAPI(k8sAPI)

	srv := gcpserver.New(gcpserver.Drivers{GKE: cloud.GKE, K8sAPI: k8sAPI})

	ts := httptest.NewUnstartedServer(srv)
	tlsCfg, err := k8spki.ServingTLSConfig([]string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("k8spki.ServingTLSConfig: %v", err)
	}
	ts.TLS = tlsCfg
	ts.StartTLS()
	t.Cleanup(ts.Close)

	k8sAPI.SetBaseURL(ts.URL)

	ctx := context.Background()

	// The SDK client trusts the httptest server's cert via ts.Client().
	svc, err := container.NewService(ctx, option.WithEndpoint(ts.URL), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("container.NewService: %v", err)
	}

	const project, location, name = "mock-project", "us-central1", "shop"
	parent := "projects/" + project + "/locations/" + location

	if _, err := svc.Projects.Locations.Clusters.Create(parent, &container.CreateClusterRequest{
		Cluster: &container.Cluster{Name: name, InitialNodeCount: 1},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	got, err := svc.Projects.Locations.Clusters.Get(parent + "/clusters/" + name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	// The advertised CA must be real, parseable PEM (the old dummy failed here).
	caPEM, err := base64.StdEncoding.DecodeString(got.MasterAuth.ClusterCaCertificate)
	if err != nil {
		t.Fatalf("advertised CA is not valid base64: %v", err)
	}
	if len(caPEM) == 0 {
		t.Fatal("advertised CA is empty")
	}

	// Build a client-go config exactly as gcloud would: endpoint + CA, no
	// skip-verify. If the CA doesn't certify the serving cert this fails at the
	// TLS handshake.
	cfg := &rest.Config{
		Host:            got.Endpoint,
		BearerToken:     "cloudemu-anonymous",
		TLSClientConfig: rest.TLSClientConfig{CAData: caPEM},
	}
	cfg.ContentType = "application/json"
	cfg.AcceptContentTypes = "application/json"
	cfg.GroupVersion = &corev1.SchemeGroupVersion
	cfg.NegotiatedSerializer = kubescheme.Codecs.WithoutConversion()

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}

	// A real operation over the validated TLS channel.
	if _, err := cs.CoreV1().ConfigMaps("default").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-check"},
		Data:       map[string]string{"ok": "true"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("client-go CreateConfigMap over validated TLS: %v", err)
	}

	got2, err := cs.CoreV1().ConfigMaps("default").Get(ctx, "tls-check", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("client-go GetConfigMap: %v", err)
	}
	if got2.Data["ok"] != "true" {
		t.Fatalf("ConfigMap round-trip mismatch: %v", got2.Data)
	}
}
