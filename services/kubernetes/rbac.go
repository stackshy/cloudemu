package kubernetes

// rbac.go implements the authorization.k8s.io/v1 SubjectAccessReview API — a
// POST-only, non-persisted, cluster-scoped "review" resource. Real clusters
// use it (via `kubectl auth can-i` and client-go's SelfSubjectAccessReview
// helpers) to ask "would this request be allowed?" without actually issuing
// it. There's nothing to store: the request is evaluated against whatever
// Roles/ClusterRoles/RoleBindings/ClusterRoleBindings already exist in the
// registry and the answer is returned inline.
//
// Evaluation follows real RBAC semantics: a request is allowed if ANY
// binding that binds the reviewed subject (user, group, or service account)
// to a (Cluster)Role has a rule whose verb/apiGroup/resource (with "*"
// wildcards) — and, if the rule restricts resourceNames, the resource name —
// match the request. RBAC has no explicit deny, so the answer is a plain
// allow/no-opinion, not allow/deny.

import (
	"net/http"
	"slices"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// apiGroupAuthorization is the API group SubjectAccessReview is served
// under. It has no persisted store (registeredResources() doesn't list it),
// so it isn't a registry group — it's dispatched directly in ServeHTTP.
const apiGroupAuthorization = "authorization.k8s.io"

// pathSubjectAccessReviews is the one path this file answers.
const pathSubjectAccessReviews = "/apis/authorization.k8s.io/v1/subjectaccessreviews"

// wildcardAll is the RBAC "matches anything" sentinel for verbs, apiGroups,
// and resources (rbacv1.VerbAll / APIGroupAll / ResourceAll all equal "*").
const wildcardAll = "*"

// authorizationResources is the discovery entry for the review API — a
// create-only, non-namespaced virtual resource. Referenced from
// discovery.go's groupVersionDiscovery.
func authorizationResources() []apiResource {
	return []apiResource{
		{"subjectaccessreviews", "subjectaccessreview", "SubjectAccessReview", false, []string{"create"}, nil},
	}
}

// serveSubjectAccessReview decodes a SubjectAccessReview, evaluates it
// against the stored RBAC objects, and echoes it back with status.allowed
// filled in.
func (s *ClusterState) serveSubjectAccessReview(w http.ResponseWriter, r *http.Request) {
	var sar authorizationv1.SubjectAccessReview
	if !readJSON(w, r, &sar) {
		return
	}

	allowed, reason := s.checkAccess(&sar.Spec)

	sar.TypeMeta = metav1.TypeMeta{Kind: "SubjectAccessReview", APIVersion: apiGroupAuthorization + "/v1"}
	sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: allowed, Reason: reason}

	writeJSON(w, http.StatusCreated, &sar)
}

// checkAccess evaluates a SubjectAccessReviewSpec's resourceAttributes
// against every RoleBinding in the request's namespace and every
// ClusterRoleBinding. A NonResourceAttributes-only review (no
// ResourceAttributes) has nothing RBAC-Role-shaped to match, so it's a
// no-opinion "not allowed" — the emulator has no non-resource-URL rules to
// evaluate.
func (s *ClusterState) checkAccess(spec *authorizationv1.SubjectAccessReviewSpec) (allowed bool, reason string) {
	if spec.ResourceAttributes == nil {
		return false, "no RBAC policy matched: nonResourceAttributes review is not evaluated"
	}

	attrs := spec.ResourceAttributes

	s.mu.RLock()
	defer s.mu.RUnlock()

	if attrs.Namespace != "" {
		if allowed, reason := s.checkRoleBindingsLocked(attrs.Namespace, spec, attrs); allowed {
			return true, reason
		}
	}

	if allowed, reason := s.checkClusterRoleBindingsLocked(spec, attrs); allowed {
		return true, reason
	}

	return false, "no RBAC policy matched"
}

// checkRoleBindingsLocked evaluates the namespace-scoped RoleBindings bound
// to a Role or ClusterRole. Callers hold s.mu.
func (s *ClusterState) checkRoleBindingsLocked(
	namespace string, spec *authorizationv1.SubjectAccessReviewSpec, attrs *authorizationv1.ResourceAttributes,
) (allowed bool, reason string) {
	st := s.reg.stores[regKey(apiGroupRBAC, "v1", "rolebindings")]
	if st == nil {
		return false, ""
	}

	for _, obj := range st.items {
		if obj.GetNamespace() != namespace {
			continue
		}

		var rb rbacv1.RoleBinding
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &rb); err != nil {
			continue
		}

		if allowed, reason := s.evaluateBindingLocked(namespace, rb.Subjects, rb.RoleRef, spec, attrs); allowed {
			return true, "allowed by RoleBinding " + namespace + "/" + rb.Name + ": " + reason
		}
	}

	return false, ""
}

// checkClusterRoleBindingsLocked evaluates the cluster-scoped
// ClusterRoleBindings, which always bind to a ClusterRole and grant access
// regardless of the request's namespace. Callers hold s.mu.
func (s *ClusterState) checkClusterRoleBindingsLocked(
	spec *authorizationv1.SubjectAccessReviewSpec, attrs *authorizationv1.ResourceAttributes,
) (allowed bool, reason string) {
	st := s.reg.stores[regKey(apiGroupRBAC, "v1", "clusterrolebindings")]
	if st == nil {
		return false, ""
	}

	for _, obj := range st.items {
		var crb rbacv1.ClusterRoleBinding
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crb); err != nil {
			continue
		}

		if allowed, reason := s.evaluateBindingLocked("", crb.Subjects, crb.RoleRef, spec, attrs); allowed {
			return true, "allowed by ClusterRoleBinding " + crb.Name + ": " + reason
		}
	}

	return false, ""
}

// evaluateBindingLocked reports whether a binding's subjects match the
// reviewed identity and, if so, whether the role it references grants the
// requested verb/group/resource/name. bindingNamespace is "" for a
// ClusterRoleBinding (cluster-scoped) or the RoleBinding's own namespace.
// Callers hold s.mu.
func (s *ClusterState) evaluateBindingLocked(
	bindingNamespace string, subjects []rbacv1.Subject, ref rbacv1.RoleRef,
	spec *authorizationv1.SubjectAccessReviewSpec, attrs *authorizationv1.ResourceAttributes,
) (allowed bool, reason string) {
	matched := false

	for _, subj := range subjects {
		if subjectMatches(subj, bindingNamespace, spec.User, spec.Groups) {
			matched = true

			break
		}
	}

	if !matched {
		return false, ""
	}

	rules, ok := s.roleRulesLocked(bindingNamespace, ref)
	if !ok {
		return false, ""
	}

	for _, rule := range rules {
		if ruleMatches(&rule, attrs.Verb, attrs.Group, attrs.Resource, attrs.Name) {
			return true, ref.Kind + " " + ref.Name
		}
	}

	return false, ""
}

// roleRulesLocked resolves a RoleRef into its rules. A "Role" is looked up
// in bindingNamespace (RoleBindings can only reference a Role in their own
// namespace); a "ClusterRole" is cluster-scoped regardless of who
// references it. Callers hold s.mu.
func (s *ClusterState) roleRulesLocked(bindingNamespace string, ref rbacv1.RoleRef) ([]rbacv1.PolicyRule, bool) {
	switch ref.Kind {
	case "Role":
		st := s.reg.stores[regKey(apiGroupRBAC, "v1", "roles")]
		if st == nil {
			return nil, false
		}

		obj, ok := st.items[objKey(bindingNamespace, ref.Name)]
		if !ok {
			return nil, false
		}

		var role rbacv1.Role
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &role); err != nil {
			return nil, false
		}

		return role.Rules, true
	case "ClusterRole":
		st := s.reg.stores[regKey(apiGroupRBAC, "v1", "clusterroles")]
		if st == nil {
			return nil, false
		}

		obj, ok := st.items[objKey("", ref.Name)]
		if !ok {
			return nil, false
		}

		var cr rbacv1.ClusterRole
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &cr); err != nil {
			return nil, false
		}

		return cr.Rules, true
	default:
		return nil, false
	}
}

// subjectMatches reports whether subj identifies the reviewed user/groups.
// A ServiceAccount subject matches the "system:serviceaccount:<ns>:<name>"
// convention client-go and kube-apiserver both use for SA identities; its
// namespace defaults to the binding's own namespace when unset (the shape
// every RoleBinding subject uses for a same-namespace ServiceAccount).
func subjectMatches(subj rbacv1.Subject, bindingNamespace, user string, groups []string) bool {
	switch subj.Kind {
	case rbacv1.UserKind:
		return subj.Name == user
	case rbacv1.GroupKind:
		return slices.Contains(groups, subj.Name)
	case rbacv1.ServiceAccountKind:
		ns := subj.Namespace
		if ns == "" {
			ns = bindingNamespace
		}

		return user == "system:serviceaccount:"+ns+":"+subj.Name
	default:
		return false
	}
}

// ruleMatches reports whether a PolicyRule authorizes verb/group/resource
// (with "*" wildcards), and — when the rule restricts resourceNames — that
// name is among them.
func ruleMatches(rule *rbacv1.PolicyRule, verb, group, resource, name string) bool {
	if !matchesWildcard(rule.Verbs, verb) {
		return false
	}

	if !matchesWildcard(rule.APIGroups, group) {
		return false
	}

	if !matchesWildcard(rule.Resources, resource) {
		return false
	}

	if len(rule.ResourceNames) > 0 && !slices.Contains(rule.ResourceNames, name) {
		return false
	}

	return true
}

// matchesWildcard reports whether val is in list, honoring RBAC's "*"
// wildcard entries.
func matchesWildcard(list []string, val string) bool {
	for _, v := range list {
		if v == wildcardAll || v == val {
			return true
		}
	}

	return false
}
