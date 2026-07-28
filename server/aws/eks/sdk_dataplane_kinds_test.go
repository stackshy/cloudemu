// End-to-end coverage for the supporting Kubernetes kinds beyond the core
// workload controllers: a real client-go drives Jobs, Ingresses, RBAC, storage,
// autoscaling, and the cluster-scoped core kinds against a cloudemu-emulated
// cluster and observes the reconcile behavior (Jobs complete, Ingresses get a
// load-balancer IP, PVCs/PVs bind). The data plane is shared, so the same holds
// for AKS and GKE.

package eks_test

import (
	"context"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//nolint:funlen // one cohesive end-to-end scenario across the supporting kinds.
func TestSDKEKSDataPlane_SupportingKinds(t *testing.T) {
	cs := runtimeClientset(t)
	ctx := context.Background()

	const ns = "kinds"
	if _, err := cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	t.Run("Job completes", func(t *testing.T) {
		completions := int32(3)
		job, err := cs.BatchV1().Jobs(ns).Create(ctx, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "import"},
			Spec: batchv1.JobSpec{
				Completions: &completions,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"job": "import"}},
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers:    []corev1.Container{{Name: "worker", Image: "busybox"}},
					},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create job: %v", err)
		}
		if job.Status.Succeeded != completions {
			t.Fatalf("job succeeded = %d, want %d", job.Status.Succeeded, completions)
		}

		pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "job=import"})
		if err != nil {
			t.Fatalf("list job pods: %v", err)
		}
		if len(pods.Items) != int(completions) {
			t.Fatalf("job pods = %d, want %d", len(pods.Items), completions)
		}
		for _, p := range pods.Items {
			if p.Status.Phase != corev1.PodSucceeded {
				t.Fatalf("pod %s phase = %s, want Succeeded", p.Name, p.Status.Phase)
			}
		}
	})

	t.Run("Ingress gets load-balancer IP", func(t *testing.T) {
		pathType := networkingv1.PathTypePrefix
		ing, err := cs.NetworkingV1().Ingresses(ns).Create(ctx, &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "web"},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{
					Host: "app.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{{
								Path:     "/",
								PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "web",
										Port: networkingv1.ServiceBackendPort{Number: 80},
									},
								},
							}},
						},
					},
				}},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create ingress: %v", err)
		}
		got := ing.Status.LoadBalancer.Ingress
		if len(got) != 1 || got[0].IP == "" {
			t.Fatalf("ingress status.loadBalancer.ingress = %+v, want one entry with an IP", got)
		}
	})

	t.Run("PVC binds", func(t *testing.T) {
		pvc, err := cs.CoreV1().PersistentVolumeClaims(ns).Create(ctx, &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "data"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create pvc: %v", err)
		}
		if pvc.Status.Phase != corev1.ClaimBound {
			t.Fatalf("pvc phase = %s, want Bound", pvc.Status.Phase)
		}
	})

	t.Run("StorageClass round-trips (cluster-scoped)", func(t *testing.T) {
		if _, err := cs.StorageV1().StorageClasses().Create(ctx, &storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: "fast"},
			Provisioner: "cloudemu.io/noop",
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create storageclass: %v", err)
		}
		if _, err := cs.StorageV1().StorageClasses().Get(ctx, "fast", metav1.GetOptions{}); err != nil {
			t.Fatalf("get storageclass: %v", err)
		}
	})

	t.Run("RBAC Role and RoleBinding round-trip", func(t *testing.T) {
		if _, err := cs.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "reader"},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"},
			}},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create role: %v", err)
		}
		if _, err := cs.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "reader-binding"},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "reader"},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "default", Namespace: ns}},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create rolebinding: %v", err)
		}
		if _, err := cs.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-reader"},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list"},
			}},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create clusterrole: %v", err)
		}
	})

	t.Run("HorizontalPodAutoscaler round-trips", func(t *testing.T) {
		minReplicas := int32(1)
		if _, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Create(ctx, &autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "web"},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "web",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 5,
			},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create hpa: %v", err)
		}
		if _, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Get(ctx, "web", metav1.GetOptions{}); err != nil {
			t.Fatalf("get hpa: %v", err)
		}
	})

	t.Run("Node round-trips (cluster-scoped)", func(t *testing.T) {
		nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatalf("list nodes: %v", err)
		}
		_ = nodes // an emulated cluster starts with no registered Node objects

		if _, err := cs.CoreV1().Nodes().Create(ctx, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-0"},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create node: %v", err)
		}
		if _, err := cs.CoreV1().Nodes().Get(ctx, "worker-0", metav1.GetOptions{}); err != nil {
			t.Fatalf("get node: %v", err)
		}
	})

	assertGroupDiscoverable(t, cs, "batch/v1", "jobs")
	assertGroupDiscoverable(t, cs, "networking.k8s.io/v1", "ingresses")
	assertGroupDiscoverable(t, cs, "rbac.authorization.k8s.io/v1", "clusterroles")
	assertGroupDiscoverable(t, cs, "storage.k8s.io/v1", "storageclasses")
}

// assertGroupDiscoverable verifies a group-version appears in discovery and
// advertises the named resource — this is what kubectl and client-go negotiate
// against before ever issuing a typed request.
func assertGroupDiscoverable(t *testing.T, cs *kubernetes.Clientset, groupVersion, resource string) {
	t.Helper()

	rl, err := cs.Discovery().ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		t.Fatalf("discovery for %s: %v", groupVersion, err)
	}
	for _, r := range rl.APIResources {
		if r.Name == resource {
			return
		}
	}
	t.Fatalf("discovery for %s does not advertise %q", groupVersion, resource)
}
