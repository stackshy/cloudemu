package kubernetes

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stackshy/cloudemu/v2/config"
)

// clusterStatePersistedFields lists the ClusterState fields whose state a
// snapshot MUST carry (or reconstruct losslessly). Every field of ClusterState
// must appear in exactly one of this set or clusterStateRuntimeFields, so a
// newly-added stateful field that nobody classified fails TestSnapshotFieldGuard.
//
//nolint:gochecknoglobals // test fixture: the reviewed persisted-field set.
var clusterStatePersistedFields = map[string]struct{}{
	"rv":              {}, // resourceVersion high-water mark (clusterSnapshot.RV)
	"nextClusterIP":   {}, // Service ClusterIP allocator (clusterSnapshot.NextClusterIP)
	"nextPodIP":       {}, // Pod IP allocator (clusterSnapshot.NextPodIP)
	"namespaces":      {}, // typed store (clusterSnapshot.Namespaces)
	"configMaps":      {}, // typed store (clusterSnapshot.ConfigMaps)
	"pods":            {}, // typed store (clusterSnapshot.Pods)
	"secrets":         {}, // typed store (clusterSnapshot.Secrets)
	"serviceAccounts": {}, // typed store (clusterSnapshot.ServiceAccounts)
	"services":        {}, // typed store (clusterSnapshot.Services)
	"deployments":     {}, // typed store (clusterSnapshot.Deployments)
	"pdbs":            {}, // typed store (clusterSnapshot.PDBs)
	"endpoints":       {}, // typed store (clusterSnapshot.Endpoints)
	"reg":             {}, // registry store items (clusterSnapshot.Registry)
}

// clusterStateRuntimeFields lists the ClusterState fields deliberately NOT
// serialized: they are runtime-only (rebuilt fresh on restore) or config
// re-injected at registration. Each carries the reason it need not persist.
//
//nolint:gochecknoglobals // test fixture: the reviewed non-persisted-field set.
var clusterStateRuntimeFields = map[string]struct{}{
	"mu":                   {}, // lock — never serialized
	"clock":                {}, // config re-injected via newClusterState
	"lifecycleProgression": {}, // config re-injected via newClusterState
	"nodeCount":            {}, // config re-injected via newClusterState; Node objects persist in the registry store
	"admissionEnabled":     {}, // config re-injected via newClusterState
	"admissionClient":      {}, // runtime *http.Client, reconstructed
	"eventIndex":           {}, // derived — rebuilt from the restored Event store
	"wNamespaces":          {}, // watch broadcaster — live channels, rebuilt fresh
	"wConfigMaps":          {}, // watch broadcaster — rebuilt fresh
	"wPods":                {}, // watch broadcaster — rebuilt fresh
	"wSecrets":             {}, // watch broadcaster — rebuilt fresh
	"wServiceAccounts":     {}, // watch broadcaster — rebuilt fresh
	"wServices":            {}, // watch broadcaster — rebuilt fresh
	"wDeployments":         {}, // watch broadcaster — rebuilt fresh
	"wEndpoints":           {}, // watch broadcaster — rebuilt fresh
}

// apiServerPersistedFields / apiServerRuntimeFields do the same classification
// for the APIServer level, so a future stateful field added there (not just on
// ClusterState) is also forced to be classified.
//
//nolint:gochecknoglobals // test fixture: the reviewed persisted-field set.
var apiServerPersistedFields = map[string]struct{}{
	"clusters": {}, // per-UID ClusterState (apiServerSnapshot.Clusters)
}

//nolint:gochecknoglobals // test fixture: the reviewed non-persisted-field set.
var apiServerRuntimeFields = map[string]struct{}{
	"mu":                   {}, // lock — never serialized
	"baseURL":              {}, // host/port differ per run — must NOT persist
	"clock":                {}, // config re-injected before restore
	"admissionEnabled":     {}, // config re-injected before restore
	"admissionClient":      {}, // runtime *http.Client
	"lifecycleProgression": {}, // config re-injected before restore
	"nodeCount":            {}, // config re-injected before restore
}

// TestSnapshotFieldGuard is the permanent completeness guard for #868: every
// field of ClusterState AND APIServer must be classified as either persisted or
// runtime/config. A newly-added field lands in neither set and fails here, naming
// the field — so a future stateful field can't silently escape persistence the
// way the provider guard (persist/completeness_test.go, blind to these plain-map
// types) would miss.
func TestSnapshotFieldGuard(t *testing.T) {
	check := func(name string, typ reflect.Type, persisted, runtime map[string]struct{}) {
		for i := range typ.NumField() {
			f := typ.Field(i).Name
			_, inP := persisted[f]
			_, inR := runtime[f]

			switch {
			case inP && inR:
				t.Errorf("%s.%s is classified as BOTH persisted and runtime — pick one", name, f)
			case !inP && !inR:
				t.Errorf("%s.%s is unclassified — add it to clusterSnapshot/apiServerSnapshot "+
					"and the persisted set, or justify it in the runtime set", name, f)
			}
		}
	}

	check("ClusterState", reflect.TypeOf(ClusterState{}), clusterStatePersistedFields, clusterStateRuntimeFields)
	check("APIServer", reflect.TypeOf(APIServer{}), apiServerPersistedFields, apiServerRuntimeFields)
}

// TestSnapshotRoundTrip populates a cluster with non-zero allocators/rv, typed
// objects, one object of EVERY registered registry kind, and a CRD + custom
// resource, then Snapshot -> Restore into a fresh APIServer and asserts the two
// snapshots are byte-identical. Byte-equality of the whole serializable surface
// proves the round-trip is lossless — a dropped store, allocator, or the CRD
// store reconstruction would diverge the bytes and fail here. Registry coverage
// is automatic: the populate loop walks registeredResources(), so a newly-added
// registry kind is exercised without touching this test.
func TestSnapshotRoundTrip(t *testing.T) {
	src := NewAPIServer()
	src.SetClock(config.NewFakeClock(time.Unix(0, 0).UTC()))

	uid, cs := src.RegisterCluster()
	populateClusterForSnapshot(t, cs)

	raw, err := src.Snapshot(context.Background(), true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := NewAPIServer()
	dst.SetClock(config.NewFakeClock(time.Unix(0, 0).UTC()))

	if err := dst.Restore(context.Background(), raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// The restored cluster must live under the SAME UID (kubeconfig identity).
	if dst.Lookup(uid) == nil {
		t.Fatalf("restored cluster not found under original UID %s", uid)
	}

	raw2, err := dst.Snapshot(context.Background(), true)
	if err != nil {
		t.Fatalf("re-Snapshot: %v", err)
	}

	if !bytes.Equal(raw, raw2) {
		t.Fatalf("snapshot round-trip is lossy:\n before=%s\n after =%s", raw, raw2)
	}
}

// TestSnapshotRestorePreservesAllocatorsAndRV asserts the scalar invariants a
// byte-diff alone wouldn't localize: rv and both IP allocators come back with
// their persisted values (not reset to the bootstrap defaults).
func TestSnapshotRestorePreservesAllocatorsAndRV(t *testing.T) {
	src := NewAPIServer()
	src.SetClock(config.NewFakeClock(time.Unix(0, 0).UTC()))

	_, cs := src.RegisterCluster()

	cs.mu.Lock()
	cs.rv = 4242
	cs.nextClusterIP = 88
	cs.nextPodIP = 909
	cs.mu.Unlock()

	raw, err := src.Snapshot(context.Background(), true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := NewAPIServer()

	if err := dst.Restore(context.Background(), raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	var got *ClusterState
	for _, uid := range clusterUIDs(dst) {
		got = dst.Lookup(uid)
	}

	if got == nil {
		t.Fatal("no restored cluster")
	}

	got.mu.Lock()
	defer got.mu.Unlock()

	if got.rv != 4242 {
		t.Errorf("rv = %d, want 4242", got.rv)
	}

	if got.nextClusterIP != 88 {
		t.Errorf("nextClusterIP = %d, want 88", got.nextClusterIP)
	}

	if got.nextPodIP != 909 {
		t.Errorf("nextPodIP = %d, want 909", got.nextPodIP)
	}

	// A list stamps its collection resourceVersion from the restored high-water
	// mark (>= every restored item's rv, the reflector invariant), and the next
	// mutating write advances FROM it — not from 0/1.
	if lv := got.clusterRVLocked(); lv != "4242" {
		t.Errorf("list resourceVersion = %s, want 4242 (restored high-water mark)", lv)
	}

	if rv := got.nextClusterRVLocked(); rv != "4243" {
		t.Errorf("next write rv = %s, want 4243 (advanced from restored rv, not reset)", rv)
	}

	// The next allocation continues from the restored allocator counters, so it
	// can't collide with an address a restored object already holds.
	if ip := got.allocateClusterIPLocked(); ip != "10.96.0.88" {
		t.Errorf("next ClusterIP = %s, want 10.96.0.88 (advanced from restored allocator)", ip)
	}

	if ip := got.allocatePodIPLocked(); ip != "10.244.3.141" {
		t.Errorf("next Pod IP = %s, want 10.244.3.141 (advanced from restored allocator)", ip)
	}
}

// TestSnapshotRestoreRejectsIncompleteSnapshot asserts the fail-loud guard: a
// cluster snapshot missing a system namespace is rejected rather than restored
// into a half-populated cluster.
func TestSnapshotRestoreRejectsIncompleteSnapshot(t *testing.T) {
	// A cluster snapshot with an empty namespaces map (no system namespaces).
	raw := []byte(`{"clusters":{"deadbeef":{"rv":1,"nextClusterIP":1,"nextPodIP":1}}}`)

	dst := NewAPIServer()
	if err := dst.Restore(context.Background(), raw); err == nil {
		t.Fatal("Restore accepted a snapshot missing the system namespaces; want failure")
	}
}

// clusterUIDs returns the registered UIDs of s (test helper; s owns the map).
func clusterUIDs(s *APIServer) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.clusters))
	for uid := range s.clusters {
		out = append(out, uid)
	}

	return out
}

// populateClusterForSnapshot fills cs with representative state across every
// persisted surface: typed objects, one object of every registered registry
// kind, a CRD with a reconstructed custom-resource store and one CR, and
// non-default allocators/rv.
func populateClusterForSnapshot(t *testing.T, cs *ClusterState) {
	t.Helper()

	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.rv = 5000
	cs.nextClusterIP = 42
	cs.nextPodIP = 314

	// Typed objects across the nine typed stores.
	cs.pods[podKey("default", "web")] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: "pod-uid", ResourceVersion: "10"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.5"},
	}
	cs.services[objKey("default", "web-svc")] = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web-svc", Namespace: "default", UID: "svc-uid", ResourceVersion: "11"},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.96.0.7"},
	}
	cs.configMaps[objKey("default", "cfg")] = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default", UID: "cfg-uid", ResourceVersion: "12"},
		Data:       map[string]string{"k": "v"},
	}

	// One object of every registered registry kind, so a dropped store diverges
	// the round-trip bytes. Cluster-scoped kinds use an empty namespace.
	for _, d := range registeredResources() {
		store := cs.reg.getStore(d.group, d.version, d.plural)
		if store == nil {
			t.Fatalf("registered kind %s/%s/%s has no store", d.group, d.version, d.plural)
		}

		ns := ""
		if d.namespaced {
			ns = "default"
		}

		obj := newRegistryObjForTest(d, ns, "sample-"+d.plural)
		store.items[objKey(ns, obj.GetName())] = obj
	}

	// A CRD whose CR store must be reconstructed BEFORE its CR loads on restore.
	crdStore := cs.reg.getStore(apiGroupExtensions, "v1", "customresourcedefinitions")
	crd := newTestCRD()
	crdStore.items[objKey("", crd.GetName())] = crd
	reconcileCRD(cs, crd) // materializes the widgets store (mirrors a real create)

	crStore := cs.reg.getStore("example.com", "v1", "widgets")
	if crStore == nil {
		t.Fatal("CRD reconcile did not materialize the custom-resource store")
	}

	cr := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata":   map[string]any{"name": "w1", "namespace": "default"},
		"spec":       map[string]any{"size": int64(3)},
	}}
	cr.SetUID(types.UID("widget-uid"))
	cr.SetResourceVersion("77")
	crStore.items[objKey("default", "w1")] = cr
}

// newRegistryObjForTest builds a minimal, stable unstructured object for a
// registered kind.
func newRegistryObjForTest(d *resourceDef, namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetAPIVersion(d.apiVersion())
	obj.SetKind(d.kind)
	obj.SetName(name)

	if namespace != "" {
		obj.SetNamespace(namespace)
	}

	obj.SetUID(types.UID(name + "-uid"))
	obj.SetResourceVersion("100")

	return obj
}

// newTestCRD builds a minimal served CRD defining example.com/v1 Widgets.
func newTestCRD() *unstructured.Unstructured {
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupExtensions + "/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": "widgets.example.com"},
		"spec": map[string]any{
			"group": "example.com",
			"scope": "Namespaced",
			"names": map[string]any{"plural": "widgets", "kind": "Widget", "listKind": "WidgetList"},
			"versions": []any{
				map[string]any{"name": "v1", "served": true, "storage": true},
			},
		},
	}}
	crd.SetUID(types.UID("crd-uid"))
	crd.SetResourceVersion("50")

	return crd
}
