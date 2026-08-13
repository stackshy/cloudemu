package kubernetes

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// reconcileBuild drives a Build to completion the way the emulator drives a Job:
// it materializes the single build Pod (<build>-build) as Succeeded and marks
// the Build Complete with start/completion timestamps. A real cluster runs an
// actual builder Pod (S2I/Docker); the emulator converges it instantly so
// `oc get builds` and `oc logs build/<name>` see a finished build. Terminal
// builds are left untouched so a re-reconcile doesn't resurrect the Pod.
func reconcileBuild(s *ClusterState, obj *unstructured.Unstructured) {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	if isTerminalBuildPhase(phase) {
		return
	}

	ns := obj.GetNamespace()
	owner := ownerRefOf(obj)

	if len(s.podsOwnedByLocked(ns, owner.UID)) == 0 {
		pod := s.buildControllerPod(ns, obj.GetName()+"-build", buildPodTemplate(obj.GetName()), owner)
		s.markPodSucceededLocked(pod)
		s.pods[podKey(ns, pod.Name)] = pod
		s.wPods.publish(EventAdded, ns, *pod.DeepCopy())
	}

	ts := s.now().Time.UTC().Format(time.RFC3339)
	_ = unstructured.SetNestedField(obj.Object, buildPhaseComplete, "status", "phase")
	_ = unstructured.SetNestedField(obj.Object, ts, "status", "startTimestamp")
	_ = unstructured.SetNestedField(obj.Object, ts, "status", "completionTimestamp")
}

// isTerminalBuildPhase reports whether a Build has reached a phase that must not
// be re-run.
func isTerminalBuildPhase(phase string) bool {
	switch phase {
	case buildPhaseComplete, "Failed", "Error", "Canceled": //nolint:goconst // one-off terminal build phases.
		return true
	default:
		return false
	}
}

// buildPhaseComplete is the terminal phase a converged Build reports.
const buildPhaseComplete = "Complete"

// buildPodTemplate synthesizes the minimal builder Pod spec a Build runs. The
// concrete builder image is irrelevant to the emulator (nothing executes); the
// Pod exists so the build surfaces in `oc get pods` with the conventional
// openshift.io/build.name label.
func buildPodTemplate(buildName string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"openshift.io/build.name": buildName}},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{Name: "docker-build", Image: "openshift/origin-docker-builder"},
			},
		},
	}
}

// buildInstantiateSuffix is the `oc start-build` subresource verb.
const buildInstantiateSuffix = "/instantiate"

// buildInstantiateTarget returns the (namespace, buildConfig) a `oc start-build`
// POST targets, or ok=false when the path is not a buildconfig instantiate.
func buildInstantiateTarget(path string) (namespace, name string, ok bool) {
	const marker = "/apis/build.openshift.io/v1/namespaces/"
	if !strings.HasPrefix(path, marker) || !strings.HasSuffix(path, buildInstantiateSuffix) {
		return "", "", false
	}

	// <ns>/buildconfigs/<name>/instantiate
	rest := strings.TrimSuffix(strings.TrimPrefix(path, marker), buildInstantiateSuffix)

	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "buildconfigs" {
		return "", "", false
	}

	return parts[0], parts[2], true
}

// serveBuildInstantiate implements `oc start-build`: it mints a Build from a
// BuildConfig (copying its spec, bumping the config's lastVersion), drives it to
// completion via reconcileBuild, and returns the Build.
func (s *ClusterState) serveBuildInstantiate(w http.ResponseWriter, namespace, bcName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bcStore := s.reg.getStore(apiGroupOSBuild, "v1", "buildconfigs")
	buildStore := s.reg.getStore(apiGroupOSBuild, "v1", "builds")

	if bcStore == nil || buildStore == nil {
		writeNotFound(w, "openshift: build stores unavailable")

		return
	}

	bc, ok := bcStore.items[objKey(namespace, bcName)]
	if !ok {
		writeNotFound(w, "openshift: buildconfig not found: "+objKey(namespace, bcName))

		return
	}

	version, _, _ := unstructured.NestedInt64(bc.Object, "status", "lastVersion")
	version++
	_ = unstructured.SetNestedField(bc.Object, version, "status", "lastVersion")
	bcStore.stampRVLocked(bc)

	build := newBuildFromConfig(bc, namespace, bcName, version)
	build.SetCreationTimestamp(s.now())
	buildStore.stampRVLocked(build)
	buildStore.items[objKey(namespace, build.GetName())] = build

	reconcileBuild(s, build)

	buildStore.watch.publish(EventAdded, namespace, *build.DeepCopy())

	writeJSON(w, http.StatusCreated, build)
}

// newBuildFromConfig builds a Build object seeded from a BuildConfig's spec,
// named <buildconfig>-<version> with the conventional build-config label.
func newBuildFromConfig(bc *unstructured.Unstructured, namespace, bcName string, version int64) *unstructured.Unstructured {
	spec, _, _ := unstructured.NestedMap(bc.Object, "spec")
	if spec == nil {
		spec = map[string]any{}
	}

	name := bcName + "-" + strconv.FormatInt(version, 10)

	build := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupOSBuild + "/v1",
		"kind":       "Build",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]any{"openshift.io/build-config.name": bcName},
			"annotations": map[string]any{
				"openshift.io/build-config.name": bcName,
				"openshift.io/build.number":      strconv.FormatInt(version, 10),
			},
		},
		"spec":   spec,
		"status": map[string]any{"phase": "New"},
	}}
	build.SetUID(types.UID(newUID()))
	build.SetGeneration(1)

	return build
}
