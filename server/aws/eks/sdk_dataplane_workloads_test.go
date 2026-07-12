// Wave 2 Phase 2 end-to-end test: real client-go deploys an entire workload
// stack — Namespace, ServiceAccount, Secret, ConfigMap, Deployment, Service,
// Pod — against an EKS cluster's data-plane endpoint, then deletes the
// cluster and verifies cascade tear-down.

package eks_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	cloudkube "github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// TestSDKEKSDataPlane_FullWorkloadStack walks through the resources a real
// app deployment uses: ServiceAccount, Secret (with StringData merge),
// ConfigMap, Deployment (apps/v1), Service (with ClusterIP allocation), Pod.
// Every step uses the real client-go SDK so wire encoding, error decoding,
// and group-version dispatch are exercised end-to-end.
//
//nolint:funlen // a single end-to-end scenario across 6 resource kinds; splitting hurts readability.
func TestSDKEKSDataPlane_FullWorkloadStack(t *testing.T) {
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
	ctx := context.Background()

	const clusterName = "wave2-workload"

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
	clientset := mustClientset(t, endpoint)

	// 1. Custom namespace — auto-creates a "default" ServiceAccount.
	if _, err := clientset.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop"}},
		metav1.CreateOptions{}); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	// 2. ServiceAccount "deployer" — explicit beside the auto "default".
	if _, err := clientset.CoreV1().ServiceAccounts("shop").Create(ctx,
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "deployer"}},
		metav1.CreateOptions{}); err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}

	saList, err := clientset.CoreV1().ServiceAccounts("shop").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("ListServiceAccounts: %v", err)
	}

	if len(saList.Items) != 2 {
		t.Fatalf("SAs in shop: got %d, want 2 (default + deployer)", len(saList.Items))
	}

	// 3. Secret with StringData — server should merge into Data.
	sec, err := clientset.CoreV1().Secrets("shop").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds"},
		StringData: map[string]string{"user": "shop", "pass": "rotated"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if string(sec.Data["user"]) != "shop" || string(sec.Data["pass"]) != "rotated" {
		t.Fatalf("StringData→Data merge: got %+v", sec.Data)
	}

	if len(sec.StringData) != 0 {
		t.Fatalf("StringData should be cleared after merge, got %+v", sec.StringData)
	}

	// 4. ConfigMap.
	if _, err := clientset.CoreV1().ConfigMaps("shop").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config"},
		Data:       map[string]string{"log_level": "info"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("CreateConfigMap: %v", err)
	}

	// 5. Deployment (apps/v1) — replicas mirrored onto status.
	var replicas int32 = 3
	dep, err := clientset.AppsV1().Deployments("shop").Create(ctx, &appsv1.Deployment{
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

	if dep.Status.Replicas != 3 || dep.Status.ReadyReplicas != 3 {
		t.Fatalf("Deployment status not mirrored: replicas=%d ready=%d",
			dep.Status.Replicas, dep.Status.ReadyReplicas)
	}

	// 6. Service — ClusterIP should be allocated from 10.96.0.0/12.
	svc, err := clientset.CoreV1().Services("shop").Create(ctx, &corev1.Service{
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

	// 7. Pod — explicit, separate from the Deployment.
	pod, err := clientset.CoreV1().Pods("shop").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "side-job"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "j", Image: "busybox"}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	if pod.Status.Phase != corev1.PodPending {
		t.Fatalf("Pod phase on create: got %q, want Pending", pod.Status.Phase)
	}

	// Cascade verification: delete the cluster, every endpoint must go away.
	if _, err := awsClient.DeleteCluster(ctx, &awseks.DeleteClusterInput{Name: aws.String(clusterName)}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		t.Fatal("client-go after DeleteCluster: expected error, got nil")
	}
}
