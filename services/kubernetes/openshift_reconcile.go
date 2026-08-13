package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// OpenShift synthesized-domain constants. A real cluster derives these from its
// ingress config (*.apps.<cluster-domain>); the emulator has no DNS, so it uses
// a stable synthetic apps domain for admitted Route hosts and the integrated
// registry's public repository — matching the SHAPE captured from a live cluster
// so tooling parsing these values sees the expected structure.
const (
	openShiftAppsDomain      = "apps.cloudemu.local"
	openShiftInternalReg     = "image-registry.openshift-image-registry.svc:5000"
	openShiftPublicRegPrefix = "default-route-openshift-image-registry." + openShiftAppsDomain
	openShiftDefaultRouter   = "default"
)

// reconcileRoute admits a Route the way the OpenShift router does: it fills an
// empty spec.host with the synthesized <name>-<namespace>.apps.<domain> and
// publishes a status.ingress entry marked Admitted=True. Real OpenShift assigns
// the host and admission asynchronously via the router; the emulator does it
// synchronously so a create is immediately reachable/inspectable, mirroring the
// captured status.ingress[].conditions[Admitted] shape.
func reconcileRoute(_ *ClusterState, obj *unstructured.Unstructured) {
	host, _, _ := unstructured.NestedString(obj.Object, "spec", "host")
	if host == "" {
		host = obj.GetName() + "-" + obj.GetNamespace() + "." + openShiftAppsDomain
		_ = unstructured.SetNestedField(obj.Object, host, "spec", "host")
	}

	wildcard, found, _ := unstructured.NestedString(obj.Object, "spec", "wildcardPolicy")
	if !found || wildcard == "" {
		wildcard = "None"
	}

	ingress := []any{map[string]any{
		"host":                    host,
		"routerName":              openShiftDefaultRouter,
		"routerCanonicalHostname": "router-" + openShiftDefaultRouter + "." + openShiftAppsDomain,
		"wildcardPolicy":          wildcard,
		"conditions": []any{
			map[string]any{"type": "Admitted", "status": "True"},
		},
	}}

	_ = unstructured.SetNestedSlice(obj.Object, ingress, "status", "ingress")
}

// reconcileDeploymentConfig converges a DeploymentConfig the way the emulator
// converges a ReplicaSet: it materializes spec.replicas Running Pods owned by
// the DC and reports them in status. It additionally maintains the
// DeploymentConfig-specific status fields — latestVersion (the rollout counter,
// started at 1 and bumped when the spec changes) and the Available/Progressing
// conditions — so `oc rollout status dc/<name>` and `oc get dc` read correctly.
//
// A real cluster interposes a ReplicationController and a deployer Pod per
// rollout; the emulator converges Pods directly (its established model for every
// workload controller), which keeps `oc get pods` correct without modeling the
// deprecated RC materialization.
func reconcileDeploymentConfig(s *ClusterState, obj *unstructured.Unstructured) {
	// Read the prior rollout counter/observed generation BEFORE setWorkloadStatus
	// overwrites observedGeneration, so latestVersion can bump on a spec change.
	prevLatest, _, _ := unstructured.NestedInt64(obj.Object, "status", "latestVersion")
	prevObserved, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")

	requested := rawReplicasOf(obj)
	desired := clampPodCount(requested)
	noteClampUnstructured(obj, requested, desired)

	ready := s.syncScaledPods(obj.GetNamespace(), obj.GetName(), ownerRefOf(obj),
		podTemplateFromUnstructured(obj), desired)
	setWorkloadStatus(obj, ready)

	latest := prevLatest

	switch {
	case latest == 0:
		latest = 1 // first rollout
	case obj.GetGeneration() > prevObserved:
		latest++ // spec changed since last observation -> new rollout
	}

	_ = unstructured.SetNestedField(obj.Object, latest, "status", "latestVersion")
	_ = unstructured.SetNestedSlice(obj.Object, deploymentConfigConditions(), "status", "conditions")

	s.resyncEndpointsForNamespaceLocked(obj.GetNamespace())
}

// deploymentConfigConditions returns the Available/Progressing conditions a
// rolled-out DeploymentConfig reports.
func deploymentConfigConditions() []any {
	return []any{
		map[string]any{"type": "Available", "status": "True",
			"message": "Deployment config has minimum availability."},
		map[string]any{"type": "Progressing", "status": "True",
			"reason": "NewReplicationControllerAvailable", "message": "replication controller successfully rolled out"},
	}
}

// reconcileImageStream fills an ImageStream's status with the integrated-registry
// repositories a real cluster synthesizes: the in-cluster
// image-registry.openshift-image-registry.svc:5000/<ns>/<name> and the public
// default-route-openshift-image-registry.<apps-domain>/<ns>/<name>. Matches the
// captured status.dockerImageRepository / publicDockerImageRepository shape.
func reconcileImageStream(_ *ClusterState, obj *unstructured.Unstructured) {
	repo := obj.GetNamespace() + "/" + obj.GetName()

	_ = unstructured.SetNestedField(obj.Object,
		openShiftInternalReg+"/"+repo, "status", "dockerImageRepository")
	_ = unstructured.SetNestedField(obj.Object,
		openShiftPublicRegPrefix+"/"+repo, "status", "publicDockerImageRepository")
}

// reconcileProject stamps the openshift.io/sa.scc.* annotations OpenShift's
// project controller injects on every project: the UID range, SELinux MCS
// labels, and supplemental groups that the restricted SCC allocates pods from.
// The emulator hands out a fixed, deterministic range (a real cluster allocates
// per-project); the point is that the annotations EXIST and parse, since tooling
// and pod admission read them. Only stamps them when absent, so a client that
// sets its own is not overwritten.
func reconcileProject(_ *ClusterState, obj *unstructured.Unstructured) {
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}

	defaults := map[string]string{
		"openshift.io/sa.scc.uid-range":           "1000000000/10000",
		"openshift.io/sa.scc.supplemental-groups": "1000000000/10000",
		"openshift.io/sa.scc.mcs":                 "s0:c10,c5",
		"openshift.io/node-selector":              "",
	}

	changed := false

	for k, v := range defaults {
		if _, ok := ann[k]; !ok {
			ann[k] = v
			changed = true
		}
	}

	if changed {
		obj.SetAnnotations(ann)
	}
}
