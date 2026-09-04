package kubernetes

// scheduling.go extends the multi-node scheduler (scheduleNodeLocked in node.go)
// with the kube-scheduler predicate/priority split beyond the v1 first-fit set
// (nodeSelector/taints/requests):
//
//   - Filtering (hard/required): required nodeAffinity, required inter-pod
//     affinity/anti-affinity keyed by topologyKey, and topologySpreadConstraints
//     with whenUnsatisfiable=DoNotSchedule. A Pod no node satisfies stays
//     Pending/Unschedulable, matching real Kubernetes.
//   - Scoring (soft/preferred): among the nodes that pass filtering, preferred
//     nodeAffinity/pod-(anti)affinity weights and topology-spread skew
//     minimization pick the best node deterministically (stable tie-break by
//     node name), replacing naive first-fit with score-then-lowest-name.
//
// tolerationSeconds and live NoExecute taint-based eviction (evicting an
// already-running Pod after its toleration expires) need a background eviction
// controller loop and remain a deferred follow-up; only schedule-time taint
// admission (podToleratesTaints, in node.go) is modeled here.

import (
	"math"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// feasibleNodesLocked returns the subset of nodes (name-sorted order preserved)
// that pass every hard scheduling predicate for pod. Callers hold s.mu.
func (s *ClusterState) feasibleNodesLocked(pod *corev1.Pod, nodes []schedNode) []schedNode {
	out := make([]schedNode, 0, len(nodes))

	for i := range nodes {
		if s.nodeFitsPodLocked(pod, &nodes[i], nodes) {
			out = append(out, nodes[i])
		}
	}

	return out
}

// nodeFitsPodLocked reports whether n satisfies every required predicate for
// pod: nodeSelector, taint tolerations, request feasibility, required
// nodeAffinity, required inter-pod (anti)affinity, and DoNotSchedule topology
// spread. Callers hold s.mu.
func (s *ClusterState) nodeFitsPodLocked(pod *corev1.Pod, n *schedNode, nodes []schedNode) bool {
	if !labelsMatch(pod.Spec.NodeSelector, n.labels) {
		return false
	}

	if !podToleratesTaints(pod.Spec.Tolerations, n.taints) {
		return false
	}

	if !s.podFitsNodeLocked(pod, n) {
		return false
	}

	if !nodeMatchesRequiredAffinity(pod, n) {
		return false
	}

	if !s.nodeSatisfiesPodAffinityLocked(pod, n, nodes) {
		return false
	}

	return s.nodeSatisfiesTopologySpreadLocked(pod, n, nodes)
}

// bestScoredNodeLocked picks the highest-scoring node among feasible: preferred
// affinity weight (higher wins), then lowest resulting topology-spread skew,
// then lowest name (feasible is already name-sorted, so an unbroken tie keeps
// the earliest). Callers hold s.mu.
func (s *ClusterState) bestScoredNodeLocked(pod *corev1.Pod, feasible, nodes []schedNode) string {
	best := 0
	bestAff := s.preferredScoreLocked(pod, &feasible[0], nodes)
	bestSkew := s.spreadScoreLocked(pod, &feasible[0], nodes)

	for i := 1; i < len(feasible); i++ {
		aff := s.preferredScoreLocked(pod, &feasible[i], nodes)
		skew := s.spreadScoreLocked(pod, &feasible[i], nodes)

		if aff > bestAff || (aff == bestAff && skew < bestSkew) {
			best, bestAff, bestSkew = i, aff, skew
		}
	}

	return feasible[best].name
}

// nodeMatchesRequiredAffinity reports whether n satisfies the pod's required
// nodeAffinity. Terms are OR-ed (any matching term suffices); an absent
// requirement is unconstrained.
func nodeMatchesRequiredAffinity(pod *corev1.Pod, n *schedNode) bool {
	aff := pod.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil {
		return true
	}

	req := aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if req == nil || len(req.NodeSelectorTerms) == 0 {
		return true
	}

	for i := range req.NodeSelectorTerms {
		if nodeSelectorTermMatches(&req.NodeSelectorTerms[i], n) {
			return true
		}
	}

	return false
}

// nodeSelectorTermMatches reports whether n satisfies a single nodeSelectorTerm:
// every matchExpression (over labels) AND every matchField (metadata.name only)
// must hold.
func nodeSelectorTermMatches(term *corev1.NodeSelectorTerm, n *schedNode) bool {
	for i := range term.MatchExpressions {
		if !nodeSelectorReqMatches(&term.MatchExpressions[i], n.labels) {
			return false
		}
	}

	for i := range term.MatchFields {
		f := &term.MatchFields[i]
		if f.Key != "metadata.name" || !nodeSelectorReqMatches(f, map[string]string{"metadata.name": n.name}) {
			return false
		}
	}

	return true
}

// nodeSelectorReqMatches evaluates one NodeSelectorRequirement against a label
// set, honoring In/NotIn/Exists/DoesNotExist/Gt/Lt.
func nodeSelectorReqMatches(req *corev1.NodeSelectorRequirement, lbls map[string]string) bool {
	val, has := lbls[req.Key]

	switch req.Operator {
	case corev1.NodeSelectorOpIn:
		return has && containsStr(req.Values, val)
	case corev1.NodeSelectorOpNotIn:
		return !has || !containsStr(req.Values, val)
	case corev1.NodeSelectorOpExists:
		return has
	case corev1.NodeSelectorOpDoesNotExist:
		return !has
	case corev1.NodeSelectorOpGt:
		return numericCompare(val, has, req.Values, true)
	case corev1.NodeSelectorOpLt:
		return numericCompare(val, has, req.Values, false)
	default:
		return false
	}
}

// numericCompare implements the Gt/Lt operators: both the label value and the
// single requirement value must parse as integers. gt selects greater-than.
func numericCompare(val string, has bool, values []string, gt bool) bool {
	if !has || len(values) != 1 {
		return false
	}

	lv, err1 := strconv.ParseInt(val, 10, 64)
	rv, err2 := strconv.ParseInt(values[0], 10, 64)

	if err1 != nil || err2 != nil {
		return false
	}

	if gt {
		return lv > rv
	}

	return lv < rv
}

// nodeSatisfiesPodAffinityLocked reports whether n satisfies every required
// inter-pod affinity term (a matching pod shares n's topology domain) and
// anti-affinity term (no matching pod shares it). Callers hold s.mu.
func (s *ClusterState) nodeSatisfiesPodAffinityLocked(pod *corev1.Pod, n *schedNode, nodes []schedNode) bool {
	aff := pod.Spec.Affinity
	if aff == nil {
		return true
	}

	if aff.PodAffinity != nil {
		for i := range aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
			if !s.podAffinityTermSatisfiedLocked(pod, n, &aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[i], true, nodes) {
				return false
			}
		}
	}

	if aff.PodAntiAffinity != nil {
		for i := range aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
			t := &aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[i]
			if !s.podAffinityTermSatisfiedLocked(pod, n, t, false, nodes) {
				return false
			}
		}
	}

	return true
}

// podAffinityTermSatisfiedLocked reports whether placing pod on n satisfies one
// (anti)affinity term. want=true (affinity) needs a matching pod in n's topology
// domain; want=false (anti-affinity) needs none. A node lacking the term's
// topologyKey can never satisfy affinity and always satisfies anti-affinity.
// Callers hold s.mu.
func (s *ClusterState) podAffinityTermSatisfiedLocked(
	pod *corev1.Pod, n *schedNode, term *corev1.PodAffinityTerm, want bool, nodes []schedNode,
) bool {
	domain, ok := n.labels[term.TopologyKey]
	if !ok {
		return !want
	}

	return s.matchingPodInDomainLocked(pod.Namespace, term, domain, nodes) == want
}

// matchingPodInDomainLocked reports whether any already-scheduled, non-terminal
// pod matching the term's labelSelector (in the term's namespaces, defaulting to
// podNS) runs on a node whose topologyKey value equals domain. Callers hold s.mu.
func (s *ClusterState) matchingPodInDomainLocked(
	podNS string, term *corev1.PodAffinityTerm, domain string, nodes []schedNode,
) bool {
	sel, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
	if err != nil {
		return false
	}

	namespaces := affinityTermNamespaces(podNS, term)
	nodeDomain := nodeDomainIndex(nodes, term.TopologyKey)

	for _, p := range s.pods {
		if p.Spec.NodeName == "" || podTerminal(p) || !namespaces[p.Namespace] {
			continue
		}

		if nodeDomain[p.Spec.NodeName] == domain && sel.Matches(labels.Set(p.Labels)) {
			return true
		}
	}

	return false
}

// affinityTermNamespaces returns the namespace set an inter-pod affinity term
// scopes to: its explicit Namespaces list, else the scheduling pod's own
// namespace. NamespaceSelector is not modeled.
func affinityTermNamespaces(podNS string, term *corev1.PodAffinityTerm) map[string]bool {
	if len(term.Namespaces) == 0 {
		return map[string]bool{podNS: true}
	}

	out := make(map[string]bool, len(term.Namespaces))
	for _, ns := range term.Namespaces {
		out[ns] = true
	}

	return out
}

// nodeSatisfiesTopologySpreadLocked reports whether placing pod on n keeps every
// DoNotSchedule spread constraint within maxSkew. Callers hold s.mu.
func (s *ClusterState) nodeSatisfiesTopologySpreadLocked(pod *corev1.Pod, n *schedNode, nodes []schedNode) bool {
	for i := range pod.Spec.TopologySpreadConstraints {
		c := &pod.Spec.TopologySpreadConstraints[i]
		if c.WhenUnsatisfiable != corev1.DoNotSchedule {
			continue
		}

		if !s.spreadConstraintOKLocked(pod, n, c, nodes) {
			return false
		}
	}

	return true
}

// spreadConstraintOKLocked reports whether hypothetically placing pod on n keeps
// constraint c's skew (max-min matching-pod count across topology domains)
// within maxSkew. A node without the constraint's topologyKey is unconstrained.
// Callers hold s.mu.
func (s *ClusterState) spreadConstraintOKLocked(
	pod *corev1.Pod, n *schedNode, c *corev1.TopologySpreadConstraint, nodes []schedNode,
) bool {
	domain, ok := n.labels[c.TopologyKey]
	if !ok {
		return true
	}

	counts := s.domainMatchCountsLocked(pod.Namespace, c, nodes)
	counts[domain]++

	maxSkew := int(c.MaxSkew)
	if maxSkew < 1 {
		maxSkew = 1
	}

	return maxSkewOf(counts) <= maxSkew
}

// domainMatchCountsLocked counts, per topology domain, the already-scheduled
// pods in podNS matching c's labelSelector. Every domain that exists among the
// nodes carrying c's topologyKey is seeded to 0 so empty domains lower the min.
// Callers hold s.mu.
func (s *ClusterState) domainMatchCountsLocked(
	podNS string, c *corev1.TopologySpreadConstraint, nodes []schedNode,
) map[string]int {
	counts := seededDomains(nodes, c.TopologyKey)

	sel, err := metav1.LabelSelectorAsSelector(c.LabelSelector)
	if err != nil {
		return counts
	}

	nodeDomain := nodeDomainIndex(nodes, c.TopologyKey)

	for _, p := range s.pods {
		if p.Spec.NodeName == "" || podTerminal(p) || p.Namespace != podNS {
			continue
		}

		if d, ok := nodeDomain[p.Spec.NodeName]; ok && sel.Matches(labels.Set(p.Labels)) {
			counts[d]++
		}
	}

	return counts
}

// preferredScoreLocked sums the pod's satisfied soft-preference weights on n:
// preferred nodeAffinity plus preferred pod affinity/anti-affinity. Callers hold
// s.mu.
func (s *ClusterState) preferredScoreLocked(pod *corev1.Pod, n *schedNode, nodes []schedNode) int64 {
	aff := pod.Spec.Affinity
	if aff == nil {
		return 0
	}

	var score int64

	if aff.NodeAffinity != nil {
		score += preferredNodeAffinityScore(aff.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution, n)
	}

	if aff.PodAffinity != nil {
		score += s.weightedPodTermScoreLocked(pod, aff.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution, n, true, nodes)
	}

	if aff.PodAntiAffinity != nil {
		score += s.weightedPodTermScoreLocked(pod, aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, n, false, nodes)
	}

	return score
}

// preferredNodeAffinityScore sums the weights of the preferred nodeAffinity
// terms whose preference matches n.
func preferredNodeAffinityScore(terms []corev1.PreferredSchedulingTerm, n *schedNode) int64 {
	var score int64

	for i := range terms {
		if nodeSelectorTermMatches(&terms[i].Preference, n) {
			score += int64(terms[i].Weight)
		}
	}

	return score
}

// weightedPodTermScoreLocked sums the weights of the weighted (anti)affinity
// terms satisfied by placing pod on n. Callers hold s.mu.
func (s *ClusterState) weightedPodTermScoreLocked(
	pod *corev1.Pod, terms []corev1.WeightedPodAffinityTerm, n *schedNode, want bool, nodes []schedNode,
) int64 {
	var score int64

	for i := range terms {
		if s.podAffinityTermSatisfiedLocked(pod, n, &terms[i].PodAffinityTerm, want, nodes) {
			score += int64(terms[i].Weight)
		}
	}

	return score
}

// spreadScoreLocked returns the total resulting skew across all of the pod's
// topology spread constraints if it were placed on n (lower is more balanced).
// Callers hold s.mu.
func (s *ClusterState) spreadScoreLocked(pod *corev1.Pod, n *schedNode, nodes []schedNode) int {
	total := 0

	for i := range pod.Spec.TopologySpreadConstraints {
		c := &pod.Spec.TopologySpreadConstraints[i]

		domain, ok := n.labels[c.TopologyKey]
		if !ok {
			continue
		}

		counts := s.domainMatchCountsLocked(pod.Namespace, c, nodes)
		counts[domain]++
		total += maxSkewOf(counts)
	}

	return total
}

// seededDomains returns a counts map with a zero entry for every distinct
// topology domain present among nodes carrying topologyKey, so empty domains
// count toward the skew's minimum.
func seededDomains(nodes []schedNode, topologyKey string) map[string]int {
	counts := map[string]int{}

	for i := range nodes {
		if d, ok := nodes[i].labels[topologyKey]; ok {
			if _, seen := counts[d]; !seen {
				counts[d] = 0
			}
		}
	}

	return counts
}

// nodeDomainIndex maps node name -> topology domain value for every node that
// carries topologyKey.
func nodeDomainIndex(nodes []schedNode, topologyKey string) map[string]string {
	out := make(map[string]string, len(nodes))

	for i := range nodes {
		if v, ok := nodes[i].labels[topologyKey]; ok {
			out[nodes[i].name] = v
		}
	}

	return out
}

// maxSkewOf returns max-min over the domain counts (0 when empty).
func maxSkewOf(counts map[string]int) int {
	if len(counts) == 0 {
		return 0
	}

	minV, maxV := math.MaxInt, math.MinInt

	for _, v := range counts {
		if v < minV {
			minV = v
		}

		if v > maxV {
			maxV = v
		}
	}

	return maxV - minV
}

// containsStr reports whether vals contains target.
func containsStr(vals []string, target string) bool {
	for _, v := range vals {
		if v == target {
			return true
		}
	}

	return false
}
