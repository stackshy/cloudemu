// Wave 2 Phase 4 end-to-end test: real client-go Informer over the
// in-memory K8s API. Proves Watch streaming works end-to-end — the
// Reflector / SharedIndexInformer machinery requires List + Watch to
// behave correctly together, including initial-state replay and
// mutation events.

package eks_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	cloudkube "github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestSDKEKSDataPlane_InformerObservesAddAndDelete drives a Phase-4
// Informer-on-ConfigMaps scenario:
//   - cluster created via real aws-sdk-go-v2.
//   - SharedIndexInformer starts; HasSynced returns true once the initial
//     LIST is replayed as ADDED events.
//   - Test mutates state (Create + Delete) via client-go.
//   - Informer's event handlers fire in the expected order.
//
// Each step bounded by a short context deadline so a regression on
// Watch never deadlocks the test binary.
//
//nolint:funlen // single Informer scenario — splitting hurts readability.
func TestSDKEKSDataPlane_InformerObservesAddAndDelete(t *testing.T) {
	cloud := cloudemu.NewAWS()

	k8sAPI := cloudkube.NewAPIServer()
	cloud.EKS.SetK8sAPI(k8sAPI)

	srv := awsserver.New(awsserver.Drivers{EKS: cloud.EKS, K8sAPI: k8sAPI})
	ts := httptest.NewServer(srv)

	t.Cleanup(ts.Close)
	k8sAPI.SetBaseURL(ts.URL)

	awsClient := newEKSClient(t, ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const clusterName = "wave4-watch"

	if _, err := awsClient.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String(clusterName),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{"subnet-a", "subnet-b"},
		},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := awsClient.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String(clusterName)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	endpoint := aws.ToString(out.Cluster.Endpoint)
	cs := mustClientset(t, endpoint)

	// Seed a configmap BEFORE the informer starts so the initial-list path
	// is exercised.
	if _, err := cs.CoreV1().ConfigMaps("default").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "preexisting"},
		Data:       map[string]string{"k": "seed"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed CreateConfigMap: %v", err)
	}

	// Build a namespace-scoped factory + ConfigMap informer.
	factory := informers.NewSharedInformerFactoryWithOptions(cs, time.Hour,
		informers.WithNamespace("default"))
	informer := factory.Core().V1().ConfigMaps().Informer()

	var (
		mu      sync.Mutex
		added   []string
		deleted []string
	)

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			cm, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return
			}

			mu.Lock()
			added = append(added, cm.Name)
			mu.Unlock()
		},
		DeleteFunc: func(obj any) {
			cm, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return
			}

			mu.Lock()
			deleted = append(deleted, cm.Name)
			mu.Unlock()
		},
	}); err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}

	stop := make(chan struct{})

	defer close(stop)

	factory.Start(stop)

	// Block until the initial LIST is replayed and the informer has the
	// "preexisting" configmap.
	if !cache.WaitForCacheSync(stop, informer.HasSynced) {
		t.Fatal("informer never synced")
	}

	// Mutate AFTER sync — the watch stream should fire ADDED and DELETED.
	if _, err := cs.CoreV1().ConfigMaps("default").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "live"},
		Data:       map[string]string{"k": "v"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create live: %v", err)
	}

	if err := cs.CoreV1().ConfigMaps("default").Delete(ctx, "preexisting", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("Delete preexisting: %v", err)
	}

	// Give the watch channel a moment to drain — bounded by ctx deadline.
	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		mu.Lock()
		gotAdded, gotDeleted := append([]string(nil), added...), append([]string(nil), deleted...)
		mu.Unlock()

		if containsString(gotAdded, "preexisting") &&
			containsString(gotAdded, "live") &&
			containsString(gotDeleted, "preexisting") {
			return // success
		}

		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	t.Fatalf("informer events incomplete after timeout:\n  added=%v\n  deleted=%v", added, deleted)
}

func containsString(xs []string, want string) bool {
	for _, s := range xs {
		if s == want {
			return true
		}
	}

	return false
}
