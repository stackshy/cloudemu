package kubernetes

import (
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Event emission (#873). The Event store already exists (a registry kind with
// involvedObject/reason/type field selectors); this file adds the emission the
// controllers were missing. cloudemu reconciles synchronously, so emitted events
// are deduplicated by (involvedObject, reason, message, type) into a single
// aggregated Event whose count/lastTimestamp advance — otherwise a hot reconcile
// would spam identical events. The store is capped so it can never grow
// unbounded. Every timestamp comes from the cluster clock (config.Clock) so a
// FakeClock keeps them deterministic.

const (
	eventTypeNormal = "Normal"

	// eventSourceComponent labels the emulator as the reporting controller.
	eventSourceComponent = "cloudemu"

	// maxStoredEvents bounds the Event store. Aggregation keeps the live count
	// low in practice; the cap is a backstop against a pathological reconcile.
	maxStoredEvents = 2000
)

// recordEventLocked emits (or aggregates) a Normal core/v1 Event about involved.
// A repeated (involvedObject, reason, message) increments the existing Event's
// count and lastTimestamp; a new combination creates a fresh Event. Every event
// the emulator emits today is Normal (there are no failure paths — scheduling and
// image "pulls" always succeed); Warning emission is a follow-up. Callers hold
// s.mu.
//
//nolint:gocritic // hugeParam: k8s ObjectReference, copy is intentional.
func (s *ClusterState) recordEventLocked(involved corev1.ObjectReference, reason, message string) {
	store := s.reg.getStore("", "v1", "events")
	if store == nil {
		return
	}

	now := s.now()
	nowStr := now.UTC().Format(time.RFC3339)
	key := eventDedupKey(involved, reason, message)

	if itemKey, ok := s.eventIndex[key]; ok {
		if ev := store.items[itemKey]; ev != nil {
			count := uint64at(ev, "count")
			_ = unstructured.SetNestedField(ev.Object, count+1, "count")
			_ = unstructured.SetNestedField(ev.Object, nowStr, "lastTimestamp")
			s.stampRegistryRVLocked(ev)
			store.watch.publish(EventModified, ev.GetNamespace(), *ev.DeepCopy())

			return
		}
	}

	name := involved.Name + "." + shortID()
	itemKey := objKey(involved.Namespace, name)

	ev := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata": map[string]any{
			"name":      name,
			"namespace": involved.Namespace,
		},
		"involvedObject": map[string]any{
			"apiVersion":      involved.APIVersion,
			"kind":            involved.Kind,
			"namespace":       involved.Namespace,
			"name":            involved.Name,
			"uid":             string(involved.UID),
			"resourceVersion": involved.ResourceVersion,
		},
		"reason":         reason,
		"message":        message,
		"type":           eventTypeNormal,
		"source":         map[string]any{"component": eventSourceComponent},
		"count":          int64(1),
		"firstTimestamp": nowStr,
		"lastTimestamp":  nowStr,
	}}
	ev.SetUID(types.UID(newUID()))
	ev.SetCreationTimestamp(now)
	s.stampRegistryRVLocked(ev)
	store.items[itemKey] = ev
	s.eventIndex[key] = itemKey

	store.watch.publish(EventAdded, involved.Namespace, *ev.DeepCopy())

	s.capEventsLocked(store)
}

// eventDedupKey is the aggregation key: involvedObject identity + reason +
// message. NUL-separated so no field-value collision can forge a key.
//
//nolint:gocritic // hugeParam: k8s ObjectReference, copy is intentional.
func eventDedupKey(involved corev1.ObjectReference, reason, message string) string {
	return involved.Namespace + "\x00" + involved.Kind + "\x00" + involved.Name + "\x00" +
		string(involved.UID) + "\x00" + reason + "\x00" + message
}

// capEventsLocked evicts the oldest Events (by firstTimestamp) once the store
// exceeds maxStoredEvents, keeping the emitter bounded. Callers hold s.mu.
func (s *ClusterState) capEventsLocked(store *registryStore) {
	if len(store.items) <= maxStoredEvents {
		return
	}

	type aged struct {
		key   string
		stamp string
	}

	all := make([]aged, 0, len(store.items))
	for k, ev := range store.items {
		all = append(all, aged{key: k, stamp: ustr(ev, "firstTimestamp")})
	}

	sort.Slice(all, func(i, j int) bool { return all[i].stamp < all[j].stamp })

	for _, a := range all[:len(store.items)-maxStoredEvents] {
		ev := store.items[a.key]
		delete(store.items, a.key)
		delete(s.eventIndex, eventDedupKeyFromEvent(ev))
	}
}

// eventDedupKeyFromEvent reconstructs an Event's aggregation key so a capped
// (evicted) Event's index entry is removed alongside it.
func eventDedupKeyFromEvent(ev *unstructured.Unstructured) string {
	ref := corev1.ObjectReference{
		Kind:      ustr(ev, "involvedObject", "kind"),
		Namespace: ustr(ev, "involvedObject", "namespace"),
		Name:      ustr(ev, "involvedObject", "name"),
		UID:       types.UID(ustr(ev, "involvedObject", "uid")),
	}

	return eventDedupKey(ref, ustr(ev, "reason"), ustr(ev, "message"))
}

// objectReferenceForUnstructured builds the involvedObject reference for a
// registry-backed object (workload controllers, jobs, cronjobs).
func objectReferenceForUnstructured(u *unstructured.Unstructured) corev1.ObjectReference {
	return corev1.ObjectReference{
		APIVersion:      u.GetAPIVersion(),
		Kind:            u.GetKind(),
		Namespace:       u.GetNamespace(),
		Name:            u.GetName(),
		UID:             u.GetUID(),
		ResourceVersion: u.GetResourceVersion(),
	}
}

// objectReferenceForPod builds the involvedObject reference for a typed Pod
// (kubelet/scheduler events).
func objectReferenceForPod(p *corev1.Pod) corev1.ObjectReference {
	return corev1.ObjectReference{
		APIVersion:      "v1",
		Kind:            kindPod,
		Namespace:       p.Namespace,
		Name:            p.Name,
		UID:             p.UID,
		ResourceVersion: p.ResourceVersion,
	}
}

// objectReferenceForOwner builds an ObjectReference for a controller from the
// OwnerReference its Pods carry — used when a controller emits an event (e.g.
// SuccessfulCreate) about itself while materializing Pods.
//
//nolint:gocritic // hugeParam: k8s OwnerReference, copy is intentional.
func objectReferenceForOwner(owner metav1.OwnerReference, namespace string) corev1.ObjectReference {
	return corev1.ObjectReference{
		APIVersion: owner.APIVersion,
		Kind:       owner.Kind,
		Namespace:  namespace,
		Name:       owner.Name,
		UID:        owner.UID,
	}
}

// ownerReferenceForMeta builds an ObjectReference from a typed workload's meta,
// used when a controller emits an event about itself.
func ownerReferenceForMeta(apiVersion, kind string, meta *metav1.ObjectMeta) corev1.ObjectReference {
	return corev1.ObjectReference{
		APIVersion:      apiVersion,
		Kind:            kind,
		Namespace:       meta.Namespace,
		Name:            meta.Name,
		UID:             meta.UID,
		ResourceVersion: meta.ResourceVersion,
	}
}
