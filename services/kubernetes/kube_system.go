package kubernetes

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// A fresh real cluster never boots empty: kube-system already runs coredns
// (cluster DNS), the kube-dns Service that fronts it, and a kube-proxy DaemonSet.
// Seeding these as plain objects that ride the existing generic reconcile makes
// `kubectl -n kube-system get pods/deploy/ds/svc` look like a managed cluster
// (EKS/AKS/GKE) instead of a bare object store — without any bespoke controller.
// They are cosmetic: coredns does not actually resolve DNS (there is no real
// resolver), it just makes the cluster look real to tooling.
//
// Control-plane static pods (apiserver/scheduler/controller-manager/etcd) are
// intentionally omitted — managed clusters hide them, which is the look we want.

const (
	// kubeDNSName is the shared name of the coredns Deployment's fronting Service
	// and the workload's k8s-app label value.
	kubeDNSName      = "kube-dns"
	kubeDNSLabelKey  = "k8s-app"
	corednsName      = "coredns"
	kubeProxyName    = "kube-proxy"
	namespaceKubeSys = "kube-system"

	// corednsReplicas mirrors the default two-replica coredns Deployment a
	// kubeadm/EKS/GKE cluster ships with.
	corednsReplicas = 2

	// kubeDNSClusterIPOffset is the well-known ClusterIP offset the kube-dns
	// Service is pinned to (10.96.0.10), the value kubelet hands pods as their
	// DNS server. The ClusterIP allocator's monotonic counter is bumped past it
	// so a client Service never collides with (or re-hands out) .10.
	kubeDNSClusterIPOffset uint32 = 10
	kubeDNSClusterIP              = "10.96.0.10"

	// coredns / kube-proxy container images. Cosmetic — nothing pulls them.
	corednsImage   = "registry.k8s.io/coredns/coredns:v1.11.1"
	kubeProxyImage = "registry.k8s.io/kube-proxy:" + nodeKubeletVersion

	// kube-dns Service port numbers (DNS over UDP+TCP, plus the coredns metrics
	// port), matching a real cluster's kube-dns Service.
	dnsPort     = 53
	metricsPort = 9153
)

// seedKubeSystemLocked populates kube-system with the standard cluster add-ons
// (coredns Deployment + kube-dns Service + kube-proxy DaemonSet) as ordinary
// objects driven through the existing reconcile paths. Default-on, deterministic.
// Callers hold s.mu (invoked only from newClusterState, single-threaded).
func (s *ClusterState) seedKubeSystemLocked() {
	// coredns first: its reconcile materializes the Running pods the kube-dns
	// Service's endpoints then populate from.
	s.seedCoreDNSDeploymentLocked()
	s.seedKubeDNSServiceLocked()
	s.seedKubeProxyDaemonSetLocked()
}

// seedCoreDNSDeploymentLocked stores the coredns Deployment and reconciles it so
// two Running coredns pods come up under the k8s-app=kube-dns label.
func (s *ClusterState) seedCoreDNSDeploymentLocked() {
	labels := map[string]string{kubeDNSLabelKey: kubeDNSName}
	replicas := int32(corednsReplicas)

	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{Kind: "Deployment", APIVersion: apiVersionAppsV1},
		ObjectMeta: metav1.ObjectMeta{
			Name:              corednsName,
			Namespace:         namespaceKubeSys,
			UID:               types.UID(newUID()),
			CreationTimestamp: s.now(),
			ResourceVersion:   s.nextClusterRVLocked(),
			Generation:        1,
			Labels:            labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: corednsName, Image: corednsImage}},
				},
			},
		},
	}

	s.deployments[deploymentKey(namespaceKubeSys, corednsName)] = dep
	s.reconcileDeploymentLocked(dep)
}

// seedKubeDNSServiceLocked stores the kube-dns Service with its ClusterIP pinned
// to 10.96.0.10, bumps the allocator past that offset so no client Service can
// collide with it, and populates its endpoints from the coredns pods.
func (s *ClusterState) seedKubeDNSServiceLocked() {
	labels := map[string]string{kubeDNSLabelKey: kubeDNSName}

	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: apiVersionV1},
		ObjectMeta: metav1.ObjectMeta{
			Name:              kubeDNSName,
			Namespace:         namespaceKubeSys,
			UID:               types.UID(newUID()),
			CreationTimestamp: s.now(),
			ResourceVersion:   s.nextClusterRVLocked(),
			Labels:            labels,
		},
		Spec: corev1.ServiceSpec{
			Type:       corev1.ServiceTypeClusterIP,
			ClusterIP:  kubeDNSClusterIP,
			ClusterIPs: []string{kubeDNSClusterIP},
			Selector:   labels,
			Ports: []corev1.ServicePort{
				{Name: "dns", Port: dnsPort, Protocol: corev1.ProtocolUDP, TargetPort: intstr.FromInt(dnsPort)},
				{Name: "dns-tcp", Port: dnsPort, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(dnsPort)},
				{Name: "metrics", Port: metricsPort, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(metricsPort)},
			},
		},
	}

	// Pin: never re-hand .10, and keep client allocations contiguous above it.
	if s.nextClusterIP <= kubeDNSClusterIPOffset {
		s.nextClusterIP = kubeDNSClusterIPOffset + 1
	}

	s.services[serviceKey(namespaceKubeSys, kubeDNSName)] = svc
	s.endpoints[endpointsKey(namespaceKubeSys, kubeDNSName)] = s.newEndpointsObject(namespaceKubeSys, kubeDNSName)
	s.reconcileServiceEndpointsLocked(svc)
}

// seedKubeProxyDaemonSetLocked stores kube-proxy as a real DaemonSet object (not
// a hand-written static pod) so the generic DaemonSet reconcile yields one pod
// per node — one today, and N automatically once multi-node scheduling lands.
func (s *ClusterState) seedKubeProxyDaemonSetLocked() {
	store := s.reg.getStore(apiGroupApps, "v1", "daemonsets")
	if store == nil {
		return
	}

	labels := map[string]any{kubeDNSLabelKey: kubeProxyName}

	ds := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersionAppsV1,
		"kind":       "DaemonSet",
		"metadata": map[string]any{
			"name":      kubeProxyName,
			"namespace": namespaceKubeSys,
			"labels":    map[string]any{kubeDNSLabelKey: kubeProxyName},
		},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": labels},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": kubeProxyName, "image": kubeProxyImage}},
				},
			},
		},
	}}
	ds.SetUID(types.UID(newUID()))
	ds.SetCreationTimestamp(s.now())
	ds.SetGeneration(1)
	s.stampRegistryRVLocked(ds)

	store.items[objKey(namespaceKubeSys, kubeProxyName)] = ds
	reconcileDaemonSet(s, ds)
}
