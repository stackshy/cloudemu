package kubernetes

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// OpenShift authorization reviews. `oc login` (and `oc policy`, the console)
// POST these non-persisted "review" verbs to ask "can I do X?". They are not
// registry kinds — each is a POST-only RPC with a bespoke response shape.
//
// cloudemu is an unauthenticated, allow-all backend, so every review resolves
// permissively: access reviews return allowed=true, rules reviews return a
// full-access rule. This is what lets `oc login` finish (it checks whether the
// user may list projectrequests) instead of erroring on a 404.
const apiPrefixOSAuthzV1 = "/apis/authorization.openshift.io/v1/"

// maxReviewBodyBytes caps how much of a review request body is read when
// resolving the target namespace.
const maxReviewBodyBytes = 1 << 20

// selfSubjectReviewPath is the authentication.k8s.io self-review `oc whoami`
// probes; answering it avoids oc's noisy fallback path.
const selfSubjectReviewPath = "/apis/authentication.k8s.io/v1/selfsubjectreviews"

// openShiftReviewPlurals are the authorization.openshift.io/v1 review verbs
// answered permissively.
//
//nolint:gochecknoglobals // immutable lookup set.
var openShiftReviewPlurals = map[string]bool{
	"subjectaccessreviews":       true,
	"localsubjectaccessreviews":  true,
	"resourceaccessreviews":      true,
	"localresourceaccessreviews": true,
	"selfsubjectrulesreviews":    true,
	"subjectrulesreviews":        true,
}

// openShiftReviewKind returns the review plural a POST path targets under
// authorization.openshift.io/v1 (cluster-scoped or /namespaces/<ns>/<review>),
// or "" if the path is not a review.
func openShiftReviewKind(path string) string {
	if !strings.HasPrefix(path, apiPrefixOSAuthzV1) {
		return ""
	}

	plural := path[strings.LastIndex(path, "/")+1:]
	if openShiftReviewPlurals[plural] {
		return plural
	}

	return ""
}

// serveOpenShiftReview answers an authorization.openshift.io review permissively.
func (s *ClusterState) serveOpenShiftReview(w http.ResponseWriter, r *http.Request, plural string) {
	ns := reviewNamespace(r)

	const gv = apiGroupOSAuthorization + "/v1"

	switch plural {
	case "subjectaccessreviews", "localsubjectaccessreviews":
		writeJSON(w, http.StatusOK, map[string]any{
			"kind": "SubjectAccessReviewResponse", "apiVersion": gv,
			"namespace": ns, "allowed": true, "reason": "cloudemu allows all",
		})
	case "resourceaccessreviews", "localresourceaccessreviews":
		writeJSON(w, http.StatusOK, map[string]any{
			"kind": "ResourceAccessReviewResponse", "apiVersion": gv,
			"namespace": ns, "users": []any{s.userForRequest(r)}, "groups": []any{"system:authenticated"},
		})
	default: // selfsubjectrulesreviews, subjectrulesreviews
		writeJSON(w, http.StatusOK, map[string]any{
			"kind": "SelfSubjectRulesReview", "apiVersion": gv,
			"status": map[string]any{
				"rules": []any{
					map[string]any{"verbs": []any{"*"}, "apiGroups": []any{"*"}, "resources": []any{"*"}},
				},
			},
		})
	}
}

// serveSelfSubjectReview answers the authentication.k8s.io SelfSubjectReview
// `oc whoami` posts, echoing the caller's resolved identity.
func (s *ClusterState) serveSelfSubjectReview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": "SelfSubjectReview", "apiVersion": "authentication.k8s.io/v1",
		"status": map[string]any{
			"userInfo": map[string]any{
				"username": s.userForRequest(r),
				"groups":   []any{"system:authenticated", "system:authenticated:oauth"},
			},
		},
	})
}

// reviewNamespace pulls the namespace a review targets: the /namespaces/<ns>/
// path segment for a local review, else the body's namespace field.
func reviewNamespace(r *http.Request) string {
	if i := strings.Index(r.URL.Path, "/namespaces/"); i >= 0 {
		rest := r.URL.Path[i+len("/namespaces/"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rest[:j]
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxReviewBodyBytes))
	if err != nil {
		return ""
	}

	var probe struct {
		Namespace string `json:"namespace"`
	}

	_ = json.Unmarshal(body, &probe)

	return probe.Namespace
}
