// Package resourcemanager implements the cloudresourcemanager.googleapis.com v1
// project-level IAM policy surface as a server.Handler:
//
//	POST /v1/projects/{project}:getIamPolicy
//	POST /v1/projects/{project}:setIamPolicy
//	POST /v1/projects/{project}:testIamPermissions
//
// These are the endpoints Terraform's google_project_iam_member,
// google_project_iam_binding, google_project_iam_policy and
// google_project_iam_audit_config drive via read-modify-write with an etag,
// and the ones google.golang.org/api/cloudresourcemanager/v1 clients call.
//
// The project policy has no portable driver — like the SA-level policy in the
// iam handler, it is a wire-only concern tracked here in memory, keyed by
// project id. Bindings (with conditions) and audit configs are stored verbatim
// so a get→modify→set round-trips unchanged.
package resourcemanager

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	pathPrefix      = "/v1/projects/"
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20

	getIamPolicyVerb       = "getIamPolicy"
	setIamPolicyVerb       = "setIamPolicy"
	testIamPermissionsVerb = "testIamPermissions"
)

// Handler serves the project-level IAM policy verbs. It owns only the
// per-project policy store; there is no driver behind it.
type Handler struct {
	mu       sync.RWMutex
	policies map[string]*policy // project id -> policy
	versions map[string]uint64  // project id -> monotonic write counter (etag source)
}

// New returns a project-IAM handler.
func New() *Handler {
	return &Handler{
		policies: make(map[string]*policy),
		versions: make(map[string]uint64),
	}
}

// Matches claims only POSTs to /v1/projects/{project}:{getIamPolicy|
// setIamPolicy|testIamPermissions}. The single-segment guard (no '/' in the
// tail) keeps it disjoint from the iam handler (serviceAccounts|roles paths),
// Firestore (/v1/projects/{p}/databases/…) and every other /v1/projects/
// handler, so registration order among them is unconstrained — but it must be
// registered ahead of Firestore, whose permissive prefix would otherwise
// swallow the colon-suffixed verb.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	tail := strings.TrimPrefix(r.URL.Path, pathPrefix)
	if strings.Contains(tail, "/") {
		return false
	}

	i := strings.LastIndex(tail, ":")
	if i < 0 {
		return false
	}

	switch tail[i+1:] {
	case getIamPolicyVerb, setIamPolicyVerb, testIamPermissionsVerb:
		return true
	default:
		return false
	}
}

// ServeHTTP parses "{project}:{verb}" and dispatches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, pathPrefix)

	i := strings.LastIndex(tail, ":")
	if i < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "malformed project IAM path")
		return
	}

	project, verb := tail[:i], tail[i+1:]

	switch verb {
	case getIamPolicyVerb:
		h.getIamPolicy(w, r, project)
	case setIamPolicyVerb:
		h.setIamPolicy(w, r, project)
	case testIamPermissionsVerb:
		h.testIamPermissions(w, r)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+verb)
	}
}

// getIamPolicy returns the stored policy, or an empty versioned policy with a
// stable etag when none has been set (real GCP never 404s getIamPolicy on an
// existing project).
func (h *Handler) getIamPolicy(w http.ResponseWriter, r *http.Request, project string) {
	var req getIamPolicyRequest
	_ = decodeOptional(r, &req) // options are optional

	h.mu.RLock()
	pol := h.policies[project]
	h.mu.RUnlock()

	if pol == nil {
		writeJSON(w, &policy{Version: 1, Etag: encodeEtag(iamEtagInitialVersion)})
		return
	}

	writeJSON(w, pol)
}

// setIamPolicy enforces optimistic concurrency: when a policy already exists,
// the request policy.etag must match the stored etag or the write is rejected
// with 409 ABORTED — the read-modify-write contract Terraform relies on. Each
// accepted write bumps a per-project version so successive states get distinct
// etags.
func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, project string) {
	var req setIamPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()

	if cur := h.policies[project]; cur != nil && req.Policy.Etag != cur.Etag {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, "ABORTED",
			"there were concurrent policy changes; please retry the whole "+
				"read-modify-write with the new etag")

		return
	}

	pol := req.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	// The unset-policy get reports the initial version (encodeEtag below), so a
	// write must advance PAST it — otherwise the first write would echo the same
	// etag the caller just read, defeating stale-etag detection on the next
	// write. Seed the counter to the initial version on first write, then bump.
	if h.versions[project] == 0 {
		h.versions[project] = iamEtagInitialVersion
	}

	h.versions[project]++
	pol.Etag = encodeEtag(h.versions[project])
	h.policies[project] = &pol

	h.mu.Unlock()

	writeJSON(w, &pol)
}

// testIamPermissions echoes the requested permissions. The emulator has no
// request-principal identity, so the project owner is treated as holding every
// permission asked about (the same stance the iam handler takes for SAs).
func (*Handler) testIamPermissions(w http.ResponseWriter, r *http.Request) {
	var req testIamPermissionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	out := testIamPermissionsResponse{}
	if len(req.Permissions) > 0 {
		out.Permissions = req.Permissions
	}

	writeJSON(w, &out)
}

// --- wire helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

// decodeOptional decodes a body that may be empty (getIamPolicy sends {} or no
// body). A decode error is swallowed — the only field is an ignored option.
func decodeOptional(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()

	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		return err
	}

	return json.Unmarshal(raw, v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, statusStr, msg string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": status, "message": msg, "status": statusStr},
	})
}
