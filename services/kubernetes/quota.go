package kubernetes

import (
	"fmt"
	"net/http"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// quotaCountPrefix is the "count/" hard-limit key prefix real ResourceQuota
// objects use for arbitrary (non-legacy) resource kinds, e.g.
// "count/deployments.apps" or "count/replicasets.apps".
const quotaCountPrefix = "count/"

// legacyQuotaResources are the core kinds real Kubernetes lets a quota's hard
// map reference by bare plural (no "count/" prefix, no group suffix) — the
// pre-generic-quota resource set upstream never migrated onto the count/
// syntax for backward compatibility.
//
//nolint:gochecknoglobals // fixed protocol lookup set, not mutable state.
var legacyQuotaResources = map[string]bool{
	resourcePods: true, "configmaps": true, "secrets": true, "services": true,
	"replicationcontrollers": true, "resourcequotas": true, "persistentvolumeclaims": true,
}

// checkAndReserveQuota enforces every namespace-scoped ResourceQuota's object
// count against the kind being created. It returns a non-nil Status (403
// Forbidden) when persisting the new object would push any matching quota's
// used count to or past its hard limit; the caller must abandon the create
// and return the Status.
//
// On success (nil return), every ResourceQuota that tracks this resource has
// its status.used bumped to reflect the object about to be persisted, so the
// reservation is atomic with the check under the caller's held s.mu.Lock —
// this method must only be called with that lock already held.
func (s *ClusterState) checkAndReserveQuota(namespace, kind, resourcePlural string) *metav1.Status {
	store := s.reg.stores[regKey("", "v1", "resourcequotas")]
	if store == nil {
		return nil
	}

	count, group := s.quotaTargetCountLocked(namespace, kind, resourcePlural)
	keys := quotaHardKeys(resourcePlural, group)

	matches := matchingQuotas(store, namespace, keys)

	for _, m := range matches {
		limit, err := resource.ParseQuantity(m.hardValue)
		if err != nil {
			continue
		}

		if int64(count) >= limit.Value() {
			return quotaExceededStatus(m.obj.GetName(), resourcePlural, group, int64(count), limit.Value())
		}
	}

	for _, m := range matches {
		bumpQuotaUsedLocked(store, m.obj, m.key, count+1)
	}

	return nil
}

// quotaMatch is one ResourceQuota object whose hard map references the
// resource being created, via hard key key with the raw configured value.
type quotaMatch struct {
	obj       *unstructured.Unstructured
	key       string
	hardValue string
}

// matchingQuotas finds every quota in namespace whose spec.hard sets one of
// keys, returning at most one match per quota object.
func matchingQuotas(store *registryStore, namespace string, keys []string) []quotaMatch {
	var matches []quotaMatch

	for _, obj := range store.items {
		if obj.GetNamespace() != namespace {
			continue
		}

		hard, _, err := unstructured.NestedStringMap(obj.Object, "spec", "hard")
		if err != nil || hard == nil {
			continue
		}

		for _, k := range keys {
			if v, ok := hard[k]; ok {
				matches = append(matches, quotaMatch{obj: obj, key: k, hardValue: v})

				break
			}
		}
	}

	return matches
}

// quotaHardKeys returns the hard-map keys a quota could use to reference
// resourcePlural: the legacy bare-plural alias (if this is one of the
// grandfathered core kinds) plus the generic "count/<plural>[.<group>]" form.
func quotaHardKeys(resourcePlural, group string) []string {
	keys := []string{quotaCountKey(resourcePlural, group)}
	if legacyQuotaResources[resourcePlural] {
		keys = append(keys, resourcePlural)
	}

	return keys
}

func quotaCountKey(resourcePlural, group string) string {
	if group == "" {
		return quotaCountPrefix + resourcePlural
	}

	return quotaCountPrefix + resourcePlural + "." + group
}

// quotaTargetCountLocked returns the number of existing objects of kind in
// namespace, plus the resource's API group ("" for core). Pods are typed and
// counted from s.pods; everything else is looked up in the registry by
// plural. Callers hold s.mu.
func (s *ClusterState) quotaTargetCountLocked(namespace, kind, resourcePlural string) (count int, group string) {
	if kind == "Pod" {
		for _, p := range s.pods {
			if p.Namespace == namespace {
				count++
			}
		}

		return count, ""
	}

	for _, st := range s.reg.stores {
		if st.def.plural != resourcePlural {
			continue
		}

		for _, obj := range st.items {
			if obj.GetNamespace() == namespace {
				count++
			}
		}

		return count, st.def.group
	}

	return 0, ""
}

// quotaExceededStatus builds the 403 Forbidden Status a real apiserver
// returns when a create would exceed a ResourceQuota's hard object count.
func quotaExceededStatus(quotaName, resourcePlural, group string, used, limit int64) *metav1.Status {
	key := quotaCountKey(resourcePlural, group)
	msg := fmt.Sprintf("exceeded quota: %s, requested: %s=1, used: %s=%d, limited: %s=%d",
		quotaName, key, key, used, key, limit)

	return &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Code:     http.StatusForbidden,
		Reason:   metav1.StatusReasonForbidden,
		Message:  msg,
	}
}

// bumpQuotaUsedLocked records newUsed under hardKey in the quota's
// status.used (mirroring spec.hard into status.hard, as a real quota
// controller does) and publishes the change to the resourcequotas watch.
// Callers hold s.mu.
func bumpQuotaUsedLocked(store *registryStore, obj *unstructured.Unstructured, hardKey string, newUsed int) {
	if hard, _, err := unstructured.NestedStringMap(obj.Object, "spec", "hard"); err == nil && hard != nil {
		_ = unstructured.SetNestedStringMap(obj.Object, hard, "status", "hard")
	}

	used, _, err := unstructured.NestedStringMap(obj.Object, "status", "used")
	if err != nil || used == nil {
		used = map[string]string{}
	}

	used[hardKey] = strconv.Itoa(newUsed)
	_ = unstructured.SetNestedStringMap(obj.Object, used, "status", "used")

	store.stampRVLocked(obj)
	store.watch.publish(EventModified, obj.GetNamespace(), *obj.DeepCopy())
}
