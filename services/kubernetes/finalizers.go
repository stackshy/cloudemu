package kubernetes

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Finalizer-gated deletion. A real apiserver does not remove an object that
// carries metadata.finalizers on DELETE: it stamps metadata.deletionTimestamp
// and leaves the object in place (Terminating) until a controller removes the
// last finalizer, at which point the object is actually deleted. These helpers
// implement that on both the typed and registry paths.

// markForDeletion stamps deletionTimestamp (once) when meta carries finalizers
// and returns true, signaling the caller to persist the now-Terminating object
// and emit MODIFIED instead of removing it. Returns false when there are no
// finalizers, so the caller deletes immediately.
func (s *ClusterState) markForDeletion(meta *metav1.ObjectMeta) bool {
	if len(meta.Finalizers) == 0 {
		return false
	}

	if meta.DeletionTimestamp == nil {
		t := s.now()
		meta.DeletionTimestamp = &t
	}

	return true
}

// finalizersDrained reports whether a Terminating object (deletionTimestamp set)
// has had its last finalizer removed and should now be garbage-collected.
func finalizersDrained(meta *metav1.ObjectMeta) bool {
	return meta.DeletionTimestamp != nil && len(meta.Finalizers) == 0
}

// markForDeletionUnstructured is the registry-path equivalent of markForDeletion.
func (s *ClusterState) markForDeletionUnstructured(obj *unstructured.Unstructured) bool {
	if len(obj.GetFinalizers()) == 0 {
		return false
	}

	if obj.GetDeletionTimestamp() == nil {
		t := s.now()
		obj.SetDeletionTimestamp(&t)
	}

	return true
}

// finalizersDrainedUnstructured is the registry-path equivalent of
// finalizersDrained.
func finalizersDrainedUnstructured(obj *unstructured.Unstructured) bool {
	return obj.GetDeletionTimestamp() != nil && len(obj.GetFinalizers()) == 0
}
