package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

// errIncompleteSnapshot is returned by Restore when a cluster snapshot is missing
// a bootstrap invariant a well-formed snapshot always carries. Static so callers
// (and err113) get a wrappable failure.
var errIncompleteSnapshot = errors.New("kubernetes: incomplete cluster snapshot")

// Persistence of the shared Kubernetes data plane (#868). The provider side
// (EKS/AKS/GKE) already persists each cluster's name->UID mapping and recomputes
// its kubeconfig endpoint lazily at describe time; this is the missing half —
// capturing the APIServer's per-UID ClusterState so a restored kubeconfig's
// /k8s/<uid> endpoint answers with the same pods/deployments/CRDs it did before
// the restart, instead of 404-ing on an unknown cluster.
//
// The APIServer implements snapshot.Snapshottable so the logic lives next to the
// state it owns, but it is wired specially by serverkit (attached to the
// top-level Snapshot.Kubernetes field, not through the provider-keyed
// ExportAll/RestoreAll) because the data plane is shared across every cloud.

var _ snapshot.Snapshottable = (*APIServer)(nil)

// apiServerSnapshot is the serialized form of every registered cluster, keyed by
// the same UID the kubeconfig embeds — so a restore reinstates each ClusterState
// under the exact UID the provider's persisted mapping still points at.
type apiServerSnapshot struct {
	Clusters map[string]clusterSnapshot `json:"clusters,omitempty"`
}

// clusterSnapshot is one ClusterState's serializable surface. The nine typed
// maps hold upstream JSON-tagged types (map[string]*corev1.Pod, …) and the
// registry holds map[string]*unstructured.Unstructured, so every field marshals
// directly — no per-kind mirror structs. The only bespoke work is the three
// unexported scalars (rv, the two IP allocators), captured explicitly. The
// broadcasters, admissionClient, eventIndex, mutex, clock and config flags are
// deliberately absent: they are runtime-only (rebuilt fresh on restore) or
// config re-injected at registration, never serialized (see Restore).
type clusterSnapshot struct {
	// RV is the cluster-wide monotonic resourceVersion high-water mark. It MUST
	// round-trip: client-go reflectors start a watch from a list's
	// resourceVersion, which must be >= every item's, and a fresh-from-0 counter
	// after restore would hand out resourceVersions below the restored items'.
	RV uint64 `json:"rv"`
	// NextClusterIP / NextPodIP are the Service ClusterIP and Pod IP allocators.
	// Without them a post-restore create would reuse an address a restored object
	// already holds.
	NextClusterIP uint32 `json:"nextClusterIP"`
	NextPodIP     uint32 `json:"nextPodIP"`

	Namespaces      map[string]*corev1.Namespace             `json:"namespaces,omitempty"`
	ConfigMaps      map[string]*corev1.ConfigMap             `json:"configMaps,omitempty"`
	Pods            map[string]*corev1.Pod                   `json:"pods,omitempty"`
	Secrets         map[string]*corev1.Secret                `json:"secrets,omitempty"`
	ServiceAccounts map[string]*corev1.ServiceAccount        `json:"serviceAccounts,omitempty"`
	Services        map[string]*corev1.Service               `json:"services,omitempty"`
	Deployments     map[string]*appsv1.Deployment            `json:"deployments,omitempty"`
	PDBs            map[string]*policyv1.PodDisruptionBudget `json:"pdbs,omitempty"`
	Endpoints       map[string]*corev1.Endpoints             `json:"endpoints,omitempty"`

	// Registry captures every registry-backed store (the ~24 built-in kinds plus
	// any runtime CRD-defined kinds), keyed by the store's group/version/plural
	// registry key, so one generic shape covers all of them.
	Registry map[string]registryStoreSnapshot `json:"registry,omitempty"`
}

// registryStoreSnapshot is one registry store's objects. The store's def, watch
// broadcaster and lock are not serialized — the def is reconstructed from the
// built-in registration (or, for a CRD kind, from the restored CRD object) and
// the broadcaster is rebuilt fresh.
type registryStoreSnapshot struct {
	Items map[string]*unstructured.Unstructured `json:"items,omitempty"`
}

// Snapshot serializes every registered cluster's ClusterState. includeAssets is
// ignored (as the EKS/AKS/GKE control-plane snapshots already ignore it):
// Kubernetes objects are all metadata — a Secret/ConfigMap with its data dropped
// would be a broken restore, not a smaller one — and the store is small (the
// Event store is hard-capped at maxStoredEvents).
func (s *APIServer) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	s.mu.RLock()

	out := apiServerSnapshot{Clusters: make(map[string]clusterSnapshot, len(s.clusters))}
	for uid, cs := range s.clusters {
		out.Clusters[uid] = cs.snapshot()
	}

	s.mu.RUnlock()

	if len(out.Clusters) == 0 {
		out.Clusters = nil
	}

	return json.Marshal(out)
}

// snapshot deep-copies this cluster's serializable state under its read lock, so
// the returned value is fully independent of the live maps and safe to marshal
// after the lock is released (no concurrent in-place mutation can race it).
func (s *ClusterState) snapshot() clusterSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cs := clusterSnapshot{
		RV:            s.rv,
		NextClusterIP: s.nextClusterIP,
		NextPodIP:     s.nextPodIP,

		Namespaces:      copyObjMap(s.namespaces),
		ConfigMaps:      copyObjMap(s.configMaps),
		Pods:            copyObjMap(s.pods),
		Secrets:         copyObjMap(s.secrets),
		ServiceAccounts: copyObjMap(s.serviceAccounts),
		Services:        copyObjMap(s.services),
		Deployments:     copyObjMap(s.deployments),
		PDBs:            copyObjMap(s.pdbs),
		Endpoints:       copyObjMap(s.endpoints),
	}

	// The registry's set of stores is guarded by reg.mu; each store's items are
	// guarded by the ClusterState lock already held. Lock order is
	// ClusterState -> registry throughout (reconcile hooks addStore under s.mu),
	// so taking reg.mu here is consistent and deadlock-free.
	s.reg.mu.RLock()
	cs.Registry = make(map[string]registryStoreSnapshot, len(s.reg.stores))

	for key, st := range s.reg.stores {
		cs.Registry[key] = registryStoreSnapshot{Items: copyObjMap(st.items)}
	}

	s.reg.mu.RUnlock()

	return cs
}

// copyObjMap deep-copies a map of DeepCopy-able objects (every typed kind and
// unstructured expose DeepCopy() T), returning nil for an empty input so the
// JSON omitempty tags drop absent kinds.
func copyObjMap[T interface{ DeepCopy() T }](in map[string]T) map[string]T {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]T, len(in))
	for k, v := range in {
		out[k] = v.DeepCopy()
	}

	return out
}

// Restore reinstates the persisted clusters under their original UIDs. It writes
// s.clusters directly (it owns the private map) rather than widening
// RegisterCluster's random-UID-only public API. Each ClusterState is rebuilt via
// newClusterState with the APIServer's current config (clock, admission, staged
// lifecycle) re-injected — exactly the path RegisterCluster uses — then its
// persisted data is loaded on top. Restore fails loudly on a malformed snapshot
// so a partial/degenerate cluster never silently replaces a valid one.
func (s *APIServer) Restore(_ context.Context, data json.RawMessage) error {
	var snap apiServerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kubernetes: parse snapshot: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for uid := range snap.Clusters {
		cs := snap.Clusters[uid]

		state, err := s.restoreClusterLocked(&cs)
		if err != nil {
			return fmt.Errorf("kubernetes: restore cluster %s: %w", uid, err)
		}

		s.clusters[uid] = state
	}

	return nil
}

// restoreClusterLocked builds one ClusterState from a snapshot. The caller holds
// s.mu (the APIServer lock), so reading the config fields is safe. newClusterState
// pre-seeds the system namespaces/SAs/Node/Lease with FRESH UIDs; the persisted
// stores overwrite that seed wholesale (below), so no wrong-UID seeded copy can
// survive — the identity a client saw before the restart is preserved verbatim.
func (s *APIServer) restoreClusterLocked(cs *clusterSnapshot) (*ClusterState, error) {
	if err := cs.validate(); err != nil {
		return nil, err
	}

	st := newClusterState(s.clock, s.admissionEnabled, s.admissionClient, s.lifecycleProgression, s.nodeCount)

	// Wholesale-replace the typed maps: the snapshot is the source of truth, so
	// the fresh seed (including its throwaway UIDs) is discarded entirely.
	st.namespaces = nonNilMap(cs.Namespaces)
	st.configMaps = nonNilMap(cs.ConfigMaps)
	st.pods = nonNilMap(cs.Pods)
	st.secrets = nonNilMap(cs.Secrets)
	st.serviceAccounts = nonNilMap(cs.ServiceAccounts)
	st.services = nonNilMap(cs.Services)
	st.deployments = nonNilMap(cs.Deployments)
	st.pdbs = nonNilMap(cs.PDBs)
	st.endpoints = nonNilMap(cs.Endpoints)

	restoreRegistryStores(st, cs.Registry)

	// eventIndex is derived state — rebuild it from the restored Event store
	// rather than serializing it, so it can never drift from the events it
	// indexes (the dedup key is a pure function of fields stored on each Event).
	rebuildEventIndex(st)

	// Restore the resourceVersion high-water mark and the IP allocators. Guard
	// against a degenerate zero (a well-formed v4 snapshot always carries real
	// values) so a malformed field can't wedge allocation at the reserved base.
	st.rv = cs.RV

	if cs.NextClusterIP != 0 {
		st.nextClusterIP = cs.NextClusterIP
	}

	if cs.NextPodIP != 0 {
		st.nextPodIP = cs.NextPodIP
	}

	return st, nil
}

// restoreRegistryStores loads the persisted registry objects into st. CRD stores
// are reconstructed BEFORE their custom resources load: the built-in
// customresourcedefinitions store is restored first, each restored CRD object
// materializes its CR store(s) via the same crdResourceDefs path reconcileCRD
// uses, and only then are all stores (built-in + reconstructed CR stores) filled.
func restoreRegistryStores(st *ClusterState, stores map[string]registryStoreSnapshot) {
	crdKey := regKey(apiGroupExtensions, "v1", "customresourcedefinitions")

	if crdSnap, ok := stores[crdKey]; ok {
		if crdStore := st.reg.getStore(apiGroupExtensions, "v1", "customresourcedefinitions"); crdStore != nil {
			crdStore.items = nonNilMap(crdSnap.Items)

			for _, obj := range crdStore.items {
				for _, d := range crdResourceDefs(obj) {
					st.reg.addStore(d)
				}
			}
		}
	}

	for key, rs := range stores {
		store := st.reg.storeForKey(key)
		if store == nil {
			// A store this build doesn't know and that no restored CRD
			// reconstructed. Skipping mirrors persist.Restore's forward-compat
			// handling of an unrecognized service; schema gating already rejects
			// cross-format snapshots at the file level.
			continue
		}

		store.items = nonNilMap(rs.Items)
	}
}

// rebuildEventIndex reconstructs the (aggregation-key -> "<ns>/<name>") dedup
// index from the restored Event store. Callers hold the APIServer lock; st is not
// yet published, so its own lock is uncontended.
func rebuildEventIndex(st *ClusterState) {
	store := st.reg.getStore("", "v1", "events")
	if store == nil {
		return
	}

	st.eventIndex = make(map[string]string, len(store.items))
	for key, ev := range store.items {
		st.eventIndex[eventDedupKeyFromEvent(ev)] = key
	}
}

// validate rejects a structurally-broken cluster snapshot so a partial restore
// can never leave a half-populated cluster masquerading as valid. It asserts the
// system namespaces every real cluster always carries are present — the canonical
// "this is a complete cluster snapshot" marker. It does NOT require the default
// ServiceAccounts / Node / Lease individually: those are ordinary objects a user
// can legitimately delete (`kubectl delete sa default`, `delete node …`), so a
// snapshot that honestly omits a user-deleted object must still restore, and the
// wholesale typed-map replacement already eliminates any wrong-UID seeded copy
// regardless of what the snapshot contains.
func (cs *clusterSnapshot) validate() error {
	for _, ns := range []string{"default", "kube-system", "kube-public", "kube-node-lease"} {
		if cs.Namespaces[ns] == nil {
			return fmt.Errorf("%w: system namespace %q", errIncompleteSnapshot, ns)
		}
	}

	return nil
}

// nonNilMap returns m, or a fresh empty map when m is nil, so a restored
// ClusterState never holds a nil map a handler would write into.
func nonNilMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return make(map[K]V)
	}

	return m
}
