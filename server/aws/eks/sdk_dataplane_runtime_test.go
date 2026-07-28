// End-to-end runtime test: a real client-go drives a full workload stack
// against a cloudemu-emulated EKS cluster and observes minikube-like behavior —
// Deployments and StatefulSets materialize Running Pods, Services get
// Endpoints, /scale changes the Pod count, and deleting a controller cascades
// to its Pods. The data plane is shared, so the same behavior holds for AKS and
// GKE (their connect paths are covered by their own data-plane tests).

package eks_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	cloudkube "github.com/stackshy/cloudemu/v2/services/kubernetes"
)

//nolint:funlen // one cohesive end-to-end runtime scenario.
func TestSDKEKSDataPlane_WorkloadRuntime(t *testing.T) {
	cs := runtimeClientset(t)
	ctx := context.Background()

	const ns = "apps"
	if _, err := cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// --- Deployment -> Running Pods, Service -> Endpoints ---------------------
	dep, err := cs.AppsV1().Deployments(ns).Create(ctx, workloadDeployment("web", 2), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if dep.Status.ReadyReplicas != 2 {
		t.Fatalf("deployment readyReplicas = %d, want 2", dep.Status.ReadyReplicas)
	}

	assertRunningPods(t, cs, ns, "app=web", 2)

	if _, err := cs.CoreV1().Services(ns).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create service: %v", err)
	}
	assertEndpointAddresses(t, cs, ns, "web", 2)

	// --- Scale via the /scale subresource ------------------------------------
	scale, err := cs.AppsV1().Deployments(ns).GetScale(ctx, "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("GetScale: %v", err)
	}
	if scale.Spec.Replicas != 2 {
		t.Fatalf("scale.spec.replicas = %d, want 2", scale.Spec.Replicas)
	}
	scale.Spec.Replicas = 4
	if _, err := cs.AppsV1().Deployments(ns).UpdateScale(ctx, "web", scale, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateScale: %v", err)
	}
	assertRunningPods(t, cs, ns, "app=web", 4)
	assertEndpointAddresses(t, cs, ns, "web", 4) // endpoints track the new Pods

	// --- Field selector on spec.nodeName resolves ----------------------------
	// Every materialized Pod is scheduled to the synthetic node, so a
	// `--field-selector spec.nodeName=...` query (common in node-drain / kubelet
	// tooling) must return them, not an empty list.
	byNode, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app=web", FieldSelector: "spec.nodeName=cloudemu-node-0",
	})
	if err != nil {
		t.Fatalf("list pods by node: %v", err)
	}
	if len(byNode.Items) != 4 {
		t.Fatalf("pods on node cloudemu-node-0 = %d, want 4", len(byNode.Items))
	}

	// --- Rolling update: new image replaces the Pods --------------------------
	before := assertRunningPods(t, cs, ns, "app=web", 4)
	beforeUIDs := podUIDSet(before)

	upd := workloadDeployment("web", 4)
	upd.Spec.Template.Spec.Containers[0].Image = "nginx:1.28"
	if _, err := cs.AppsV1().Deployments(ns).Update(ctx, upd, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("rolling update: %v", err)
	}

	after := assertRunningPods(t, cs, ns, "app=web", 4)
	for _, p := range after {
		if got := p.Spec.Containers[0].Image; got != "nginx:1.28" {
			t.Fatalf("pod %s image = %q, want nginx:1.28 after rollout", p.Name, got)
		}
		if beforeUIDs[string(p.UID)] {
			t.Fatalf("pod %s survived the rollout; template change must replace Pods", p.Name)
		}
	}
	assertEndpointAddresses(t, cs, ns, "web", 4) // endpoints re-point at the new Pods

	// --- StatefulSet -> stable Pods + Bound PVCs ------------------------------
	if _, err := cs.AppsV1().StatefulSets(ns).Create(ctx, workloadStatefulSet("db", 3), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create statefulset: %v", err)
	}
	pods := assertRunningPods(t, cs, ns, "app=db", 3)
	for i, want := range []string{"db-0", "db-1", "db-2"} {
		if pods[i].Name != want {
			t.Fatalf("statefulset pod[%d] = %q, want %q (stable ordinal names)", i, pods[i].Name, want)
		}
	}
	pvcs, err := cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list PVCs: %v", err)
	}
	if len(pvcs.Items) != 3 {
		t.Fatalf("statefulset PVCs = %d, want 3", len(pvcs.Items))
	}
	for _, pvc := range pvcs.Items {
		if pvc.Status.Phase != corev1.ClaimBound {
			t.Fatalf("PVC %s phase = %q, want Bound", pvc.Name, pvc.Status.Phase)
		}
	}

	// --- DaemonSet -> one Pod per node ---------------------------------------
	if _, err := cs.AppsV1().DaemonSets(ns).Create(ctx, workloadDaemonSet("agent"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create daemonset: %v", err)
	}
	assertRunningPods(t, cs, ns, "app=agent", 1)

	// --- Cascade teardown -----------------------------------------------------
	if err := cs.AppsV1().Deployments(ns).Delete(ctx, "web", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	assertRunningPods(t, cs, ns, "app=web", 0)   // Pods garbage-collected
	assertEndpointAddresses(t, cs, ns, "web", 0) // endpoints drained

	if err := cs.AppsV1().StatefulSets(ns).Delete(ctx, "db", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete statefulset: %v", err)
	}
	assertRunningPods(t, cs, ns, "app=db", 0)
}

// runtimeClientset stands up the EKS control plane + shared data plane, creates
// a cluster, and returns a client-go clientset pointed at its endpoint.
func runtimeClientset(t *testing.T) *kubernetes.Clientset {
	t.Helper()

	cloud := cloudemu.NewAWS()
	k8sAPI := cloudkube.NewAPIServer()
	cloud.EKS.SetK8sAPI(k8sAPI)

	srv := awsserver.New(awsserver.Drivers{EKS: cloud.EKS, K8sAPI: k8sAPI})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	k8sAPI.SetBaseURL(ts.URL)

	awsClient := newEKSClient(t, ts.URL)
	ctx := context.Background()

	if _, err := awsClient.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("runtime-demo"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-a"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := awsClient.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("runtime-demo")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	return mustClientset(t, aws.ToString(out.Cluster.Endpoint))
}

func podUIDSet(pods []corev1.Pod) map[string]bool {
	set := make(map[string]bool, len(pods))
	for _, p := range pods {
		set[string(p.UID)] = true
	}

	return set
}

func assertRunningPods(t *testing.T, cs *kubernetes.Clientset, ns, selector string, want int) []corev1.Pod {
	t.Helper()

	pods, err := cs.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		t.Fatalf("list pods %q: %v", selector, err)
	}
	if len(pods.Items) != want {
		t.Fatalf("pods %q = %d, want %d", selector, len(pods.Items), want)
	}

	for _, p := range pods.Items {
		if want == 0 {
			continue
		}
		if p.Status.Phase != corev1.PodRunning {
			t.Fatalf("pod %s phase = %q, want Running", p.Name, p.Status.Phase)
		}
		if p.Status.PodIP == "" {
			t.Fatalf("pod %s has no Pod IP", p.Name)
		}
	}

	return pods.Items
}

func assertEndpointAddresses(t *testing.T, cs *kubernetes.Clientset, ns, name string, want int) {
	t.Helper()

	ep, err := cs.CoreV1().Endpoints(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get endpoints %s: %v", name, err)
	}

	got := 0
	for _, sub := range ep.Subsets {
		got += len(sub.Addresses)
	}
	if got != want {
		t.Fatalf("endpoints %s addresses = %d, want %d", name, got, want)
	}
}

func workloadDeployment(name string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{"app": name}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}}},
			},
		},
	}
}

func workloadStatefulSet(name string, replicas int32) *appsv1.StatefulSet {
	labels := map[string]string{"app": name}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "postgres:16"}}},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "data"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
					},
				},
			}},
		},
	}
}

func workloadDaemonSet(name string) *appsv1.DaemonSet {
	labels := map[string]string{"app": name}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "fluentd:1.17"}}},
			},
		},
	}
}
