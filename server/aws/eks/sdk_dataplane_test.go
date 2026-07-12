// Wave 2 end-to-end test: a real aws-sdk-go-v2/service/eks client creates a
// cluster against the in-memory EKS control plane; a real
// k8s.io/client-go client then talks to the in-memory Kubernetes
// data-plane through the cluster's Endpoint. The kubeconfig path is the
// missing piece v1.6.2 stubbed out — this test proves it now works.

package eks_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	cloudkube "github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestSDKEKSDataPlane_NamespaceAndConfigMap drives the full path:
//   - cloudemu serves both the EKS control plane and the Kubernetes data
//     plane on one httptest server.
//   - The real aws-sdk-go-v2 client creates a cluster.
//   - DescribeCluster's Endpoint now points at the in-memory K8s API
//     (not the *-DATAPLANE-NOT-IMPLEMENTED sentinel).
//   - client-go uses that Endpoint as its rest.Config.Host and creates a
//     Namespace + ConfigMap, then lists them back.
//   - DeleteCluster tears the K8s state down — subsequent client-go calls
//     against the same Endpoint return 404 from the unknown-cluster route.
//
//nolint:funlen // single end-to-end test; splitting hurts readability.
func TestSDKEKSDataPlane_NamespaceAndConfigMap(t *testing.T) {
	cloud := cloudemu.NewAWS()

	k8sAPI := cloudkube.NewAPIServer()
	cloud.EKS.SetK8sAPI(k8sAPI)

	srv := awsserver.New(awsserver.Drivers{
		EKS:    cloud.EKS,
		K8sAPI: k8sAPI,
	})
	ts := httptest.NewServer(srv)

	t.Cleanup(ts.Close)

	k8sAPI.SetBaseURL(ts.URL)

	awsClient := newEKSClient(t, ts.URL)

	const clusterName = "wave2-demo"

	ctx := context.Background()

	_, err := awsClient.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String(clusterName),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{"subnet-aaa", "subnet-bbb"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := awsClient.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String(clusterName)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	endpoint := aws.ToString(out.Cluster.Endpoint)
	if endpoint == "" {
		t.Fatal("DescribeCluster returned empty Endpoint")
	}

	if strings.Contains(endpoint, "DATAPLANE-NOT-IMPLEMENTED") {
		t.Fatalf("Wave 2 regression: Endpoint still points at the Wave 1 sentinel: %s", endpoint)
	}

	if !strings.HasPrefix(endpoint, ts.URL+"/k8s/") {
		t.Fatalf("Endpoint should start with %q/k8s/, got %q", ts.URL, endpoint)
	}

	// Build a client-go config that talks plain HTTP — cloudemu's K8s API
	// server is unauthenticated and uses no TLS.
	clientset := mustClientset(t, endpoint)

	// Create a fresh namespace.
	ns, err := clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "app"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("client-go CreateNamespace: %v", err)
	}

	if ns.UID == "" {
		t.Fatal("created namespace missing UID")
	}

	// Create a ConfigMap inside it.
	cm, err := clientset.CoreV1().ConfigMaps("app").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings"},
		Data:       map[string]string{"log_level": "debug"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("client-go CreateConfigMap: %v", err)
	}

	if cm.Data["log_level"] != "debug" {
		t.Fatalf("ConfigMap.Data.log_level: got %q, want debug", cm.Data["log_level"])
	}

	// List them — must include our app ns and the settings configmap.
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("client-go ListNamespaces: %v", err)
	}

	if !containsNamespace(nsList.Items, "app") {
		t.Fatalf("namespaces list does not contain 'app': %+v", nsList.Items)
	}

	cmList, err := clientset.CoreV1().ConfigMaps("app").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("client-go ListConfigMaps: %v", err)
	}

	if len(cmList.Items) != 1 || cmList.Items[0].Name != "settings" {
		t.Fatalf("configmaps list: got %+v", cmList.Items)
	}

	// Delete the cluster — the K8s state must go with it.
	_, err = awsClient.DeleteCluster(ctx, &awseks.DeleteClusterInput{Name: aws.String(clusterName)})
	if err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	// client-go against the now-orphaned Endpoint should fail.
	_, err = clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err == nil {
		t.Fatal("client-go ListNamespaces after DeleteCluster: expected error, got nil")
	}
}

func newEKSClient(t *testing.T, baseURL string) *awseks.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")),
	)
	if err != nil {
		t.Fatalf("awsconfig: %v", err)
	}

	return awseks.NewFromConfig(cfg, func(o *awseks.Options) {
		o.BaseEndpoint = aws.String(baseURL)
	})
}

// mustClientset builds a client-go Clientset that talks to host over plain
// HTTP with the stub bearer token. Mirrors the rest.Config a user would
// build from the cluster's kubeconfig.
func mustClientset(t *testing.T, host string) *kubernetes.Clientset {
	t.Helper()

	cfg := &rest.Config{
		Host:        host,
		BearerToken: "cloudemu-anonymous",
		// Force JSON wire format — the in-memory K8s server does not
		// negotiate the application/vnd.kubernetes.protobuf type that
		// real apiservers advertise.
		ContentConfig: rest.ContentConfig{
			ContentType:          "application/json",
			AcceptContentTypes:   "application/json",
			GroupVersion:         &corev1.SchemeGroupVersion,
			NegotiatedSerializer: kubescheme.Codecs.WithoutConversion(),
		},
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}

	return cs
}

func containsNamespace(items []corev1.Namespace, name string) bool {
	for i := range items {
		if items[i].Name == name {
			return true
		}
	}

	return false
}
