package kubernetes

import (
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// applyLimitRange applies every namespace LimitRange of type Container to
// pod: containers missing a request/limit for a resource the LimitRange
// defaults get that default, and every container's resources are validated
// against the LimitRange's min/max. Returns a non-nil Status (403 Forbidden)
// on the first violation found, in which case the caller must abandon the
// create. Callers hold s.mu.
func (s *ClusterState) applyLimitRange(namespace string, pod *corev1.Pod) *metav1.Status {
	store := s.reg.stores[regKey("", "v1", "limitranges")]
	if store == nil {
		return nil
	}

	for _, obj := range store.items {
		if obj.GetNamespace() != namespace {
			continue
		}

		if status := applyLimitRangeObject(obj, pod); status != nil {
			return status
		}
	}

	return nil
}

// applyLimitRangeObject applies a single LimitRange's Container-type items to
// every container of pod.
func applyLimitRangeObject(obj *unstructured.Unstructured, pod *corev1.Pod) *metav1.Status {
	rawItems, found, err := unstructured.NestedSlice(obj.Object, "spec", "limits")
	if err != nil || !found {
		return nil
	}

	for _, raw := range rawItems {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		var item corev1.LimitRangeItem
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &item); err != nil {
			continue
		}

		if item.Type != corev1.LimitTypeContainer {
			continue
		}

		for i := range pod.Spec.Containers {
			c := &pod.Spec.Containers[i]

			applyContainerDefaults(c, &item)

			if status := validateContainerLimits(obj.GetName(), pod.Name, c, &item); status != nil {
				return status
			}
		}
	}

	return nil
}

// applyContainerDefaults fills in c.Resources.Requests/Limits from item's
// default/defaultRequest for any resource the container did not itself set.
func applyContainerDefaults(c *corev1.Container, item *corev1.LimitRangeItem) {
	if len(item.Default) > 0 {
		if c.Resources.Limits == nil {
			c.Resources.Limits = corev1.ResourceList{}
		}

		for name, qty := range item.Default {
			if _, ok := c.Resources.Limits[name]; !ok {
				c.Resources.Limits[name] = qty
			}
		}
	}

	if len(item.DefaultRequest) > 0 {
		if c.Resources.Requests == nil {
			c.Resources.Requests = corev1.ResourceList{}
		}

		for name, qty := range item.DefaultRequest {
			if _, ok := c.Resources.Requests[name]; !ok {
				c.Resources.Requests[name] = qty
			}
		}
	}
}

// validateContainerLimits checks c's requests and limits against item's
// min/max, returning a 403 Forbidden Status on the first violation.
func validateContainerLimits(lrName, podName string, c *corev1.Container, item *corev1.LimitRangeItem) *metav1.Status {
	for name, qty := range c.Resources.Requests {
		if status := checkResourceBounds(lrName, podName, c.Name, "request", name, qty, item); status != nil {
			return status
		}
	}

	for name, qty := range c.Resources.Limits {
		if status := checkResourceBounds(lrName, podName, c.Name, "limit", name, qty, item); status != nil {
			return status
		}
	}

	return nil
}

func checkResourceBounds(
	lrName, podName, containerName, kind string, name corev1.ResourceName, qty resource.Quantity, item *corev1.LimitRangeItem,
) *metav1.Status {
	if minQty, ok := item.Min[name]; ok && qty.Cmp(minQty) < 0 {
		return limitRangeViolationStatus(lrName, podName, containerName, kind, name, "minimum", minQty)
	}

	if maxQty, ok := item.Max[name]; ok && qty.Cmp(maxQty) > 0 {
		return limitRangeViolationStatus(lrName, podName, containerName, kind, name, "maximum", maxQty)
	}

	return nil
}

func limitRangeViolationStatus(
	lrName, podName, containerName, kind string, resName corev1.ResourceName, bound string, boundQty resource.Quantity,
) *metav1.Status {
	msg := fmt.Sprintf(
		"pods %q is forbidden: limitrange %q: %s %s %s for container %q must be %s to %s",
		podName, lrName, kind, resName, kind, containerName, boundRelation(bound), boundQty.String(),
	)

	return &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Code:     http.StatusForbidden,
		Reason:   metav1.StatusReasonForbidden,
		Message:  msg,
	}
}

// boundRelation renders the "minimum"/"maximum" bound kind as the comparison
// phrase used in the violation message.
func boundRelation(bound string) string {
	if bound == "minimum" {
		return "greater than or equal"
	}

	return "less than or equal"
}
