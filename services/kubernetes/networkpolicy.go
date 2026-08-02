package kubernetes

// networkpolicy.go implements a QUERY API for NetworkPolicy — NOT live-traffic
// enforcement. The emulator has no packet path: Pods never actually send
// bytes to each other, so there is nothing for a NetworkPolicy to intercept
// in real time. EvaluateNetworkPolicy instead answers "would a real cluster's
// CNI allow this connection?" against whatever NetworkPolicy objects are
// currently stored, for tests (and future topology.Engine wiring — see
// topology.CanConnect for the analogous VPC/security-group query) that want
// to assert on network segmentation without a live cluster.
//
// Semantics (ingress-only; NetworkPolicy also has an Egress side that this
// query does not evaluate):
//   - No NetworkPolicy in the namespace selects the destination pod's labels
//     with a non-empty Ingress rule list -> default allow (matches real
//     Kubernetes: a Pod with no applicable NetworkPolicy accepts all traffic).
//   - At least one such policy selects the destination -> allowed only if
//     some ingress rule of some selecting policy matches both the source
//     (via the rule's "from" peers) and the port/protocol.
//
// Simplification: the query takes a single namespace for both source and
// destination (there's no cross-namespace traffic model here), so a peer's
// namespaceSelector is matched against that one namespace's own labels
// rather than a distinct source namespace. A peer's ipBlock is never
// evaluated (the query has no IP to check it against) and never matches.

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

// EvaluateNetworkPolicy reports whether traffic from a pod with srcPodLabels
// to a pod with dstPodLabels, both in namespace, on port/proto would be
// allowed by the NetworkPolicy objects currently stored for that namespace.
// port is a container port number; proto is "TCP"/"UDP"/"SCTP" ("" matches
// any protocol, mirroring an empty NetworkPolicyPort list matching everything).
//
// This is a query, not enforcement: no Pod-to-Pod traffic actually flows
// through the emulator, so nothing here blocks or allows real bytes.
func (s *ClusterState) EvaluateNetworkPolicy(
	namespace string, srcPodLabels, dstPodLabels map[string]string, port int32, proto string,
) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policies := s.selectingIngressPoliciesLocked(namespace, dstPodLabels)
	if len(policies) == 0 {
		return true
	}

	nsLabels := s.namespaceLabelsLocked(namespace)

	for i := range policies {
		if ingressAllows(policies[i].Spec.Ingress, srcPodLabels, nsLabels, port, proto) {
			return true
		}
	}

	return false
}

// namespaceLabelsLocked returns the labels of namespace, or nil if it isn't
// found or carries none. Callers hold s.mu.
func (s *ClusterState) namespaceLabelsLocked(namespace string) map[string]string {
	ns, ok := s.namespaces[namespace]
	if !ok {
		return nil
	}

	return ns.Labels
}

// selectingIngressPoliciesLocked returns every NetworkPolicy in namespace
// whose podSelector matches dstLabels and which declares at least one
// Ingress rule (a policy with no Ingress rules doesn't isolate ingress
// traffic for this query's purposes). Callers hold s.mu.
func (s *ClusterState) selectingIngressPoliciesLocked(namespace string, dstLabels map[string]string) []networkingv1.NetworkPolicy {
	st := s.reg.stores[regKey(apiGroupNetworking, "v1", "networkpolicies")]
	if st == nil {
		return nil
	}

	var out []networkingv1.NetworkPolicy

	for _, obj := range st.items {
		if obj.GetNamespace() != namespace {
			continue
		}

		var np networkingv1.NetworkPolicy
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &np); err != nil {
			continue
		}

		if len(np.Spec.Ingress) == 0 {
			continue
		}

		if !selectorMatches(&np.Spec.PodSelector, dstLabels) {
			continue
		}

		out = append(out, np)
	}

	return out
}

// ingressAllows reports whether any of the ingress rules permits traffic
// from srcLabels (in a namespace carrying nsLabels) on port/proto.
func ingressAllows(rules []networkingv1.NetworkPolicyIngressRule, srcLabels, nsLabels map[string]string, port int32, proto string) bool {
	for _, rule := range rules {
		if !portMatches(rule.Ports, port, proto) {
			continue
		}

		if len(rule.From) == 0 {
			return true
		}

		for _, peer := range rule.From {
			if peerMatchesSrc(peer, srcLabels, nsLabels) {
				return true
			}
		}
	}

	return false
}

// peerMatchesSrc reports whether a NetworkPolicyPeer selects the source pod.
// An ipBlock peer never matches (no IP is available to test against). A
// peer with neither podSelector nor namespaceSelector nor ipBlock is
// malformed and matches nothing.
func peerMatchesSrc(peer networkingv1.NetworkPolicyPeer, srcLabels, nsLabels map[string]string) bool {
	if peer.IPBlock != nil {
		return false
	}

	switch {
	case peer.PodSelector != nil && peer.NamespaceSelector != nil:
		return selectorMatches(peer.NamespaceSelector, nsLabels) && selectorMatches(peer.PodSelector, srcLabels)
	case peer.PodSelector != nil:
		return selectorMatches(peer.PodSelector, srcLabels)
	case peer.NamespaceSelector != nil:
		return selectorMatches(peer.NamespaceSelector, nsLabels)
	default:
		return false
	}
}

// portMatches reports whether port/proto is covered by ports. An empty
// ports list matches everything, matching NetworkPolicyIngressRule's
// "if this field is empty then this rule matches all ports" semantics.
func portMatches(ports []networkingv1.NetworkPolicyPort, port int32, proto string) bool {
	if len(ports) == 0 {
		return true
	}

	for _, p := range ports {
		if p.Protocol != nil && proto != "" && string(*p.Protocol) != proto {
			continue
		}

		if portValueMatches(p, port) {
			return true
		}
	}

	return false
}

// portValueMatches reports whether a single NetworkPolicyPort covers port.
// An unset Port matches every port for the (already-checked) protocol; a set
// Port matches exactly, or — with EndPort set — matches the inclusive range.
func portValueMatches(p networkingv1.NetworkPolicyPort, port int32) bool {
	if p.Port == nil {
		return true
	}

	start := p.Port.IntVal
	if p.EndPort != nil {
		return port >= start && port <= *p.EndPort
	}

	return port == start
}

// selectorMatches reports whether lbls satisfies sel (matchLabels and
// matchExpressions both honored via metav1.LabelSelectorAsSelector). A nil
// selector is treated as "no restriction" — callers only pass nil for
// NetworkPolicyPeer fields where nilness is meaningful on its own (see
// peerMatchesSrc), never for the required, non-pointer spec.podSelector.
func selectorMatches(sel *metav1.LabelSelector, lbls map[string]string) bool {
	if sel == nil {
		return true
	}

	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false
	}

	return s.Matches(labels.Set(lbls))
}
