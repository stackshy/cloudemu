package cloudfunctions

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
)

// serveIamPolicy routes the :getIamPolicy (GET) and :setIamPolicy (POST) verbs
// on a function. CloudEmu does not enforce IAM; it stores the policy so that
// Terraform's google_cloudfunctions_function_iam_member (setIamPolicy) and its
// following read (getIamPolicy) round-trip.
func (h *Handler) serveIamPolicy(w http.ResponseWriter, r *http.Request, p functionPath) {
	// Both verbs act on an existing function (matching real GCP, which never
	// serves an IAM policy for a function that does not exist).
	if _, err := h.fn.GetFunction(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	switch p.action {
	case actionGetIamPolicy:
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "getIamPolicy requires GET")
			return
		}

		h.writeIamPolicy(w, r, p.fullName())
	case actionSetIamPolicy:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "setIamPolicy requires POST")
			return
		}

		h.storeIamPolicy(w, r, p.fullName())
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

// writeIamPolicy returns the policy stored under key in the shared resource
// IAM store (version 1 with the initial etag when none is set). Shared by the
// v1 and v2 handlers.
func (h *Handler) writeIamPolicy(w http.ResponseWriter, r *http.Request, key string) {
	gcpiam.Serve(w, r, gcpiam.VerbGet, key, h.iam)
}

// storeIamPolicy writes the request policy under key with the real etag
// contract: an empty etag is a blind write, a stale one is 409 ABORTED, and
// every write mints a new etag. Shared by the v1 and v2 handlers.
func (h *Handler) storeIamPolicy(w http.ResponseWriter, r *http.Request, key string) {
	gcpiam.Serve(w, r, gcpiam.VerbSet, key, h.iam)
}

// serveTestIamPermissions answers functions/{name}:testIamPermissions (v1). Real
// GCP returns the subset of the requested permissions the caller holds; CloudEmu
// does not enforce IAM (any credential is an owner), so it echoes back the full
// requested set, the answer callers use to gate optional UI, and which
// Terraform's data.google_iam_policy tooling round-trips.
func (h *Handler) serveTestIamPermissions(w http.ResponseWriter, r *http.Request, p functionPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "testIamPermissions requires POST")
		return
	}

	if _, err := h.fn.GetFunction(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	var body testIamPermissionsRequest
	if !decodeJSON(w, r, &body, nil) {
		return
	}

	writeJSON(w, http.StatusOK, testIamPermissionsResponse(body))
}

// gen2Exists reports whether a gen2 function is stored under key.
func (h *Handler) gen2Exists(key string) bool {
	h.mu.RLock()
	_, ok := h.gen2[key]
	h.mu.RUnlock()

	return ok
}

// serveV2IamPolicy handles :getIamPolicy / :setIamPolicy on a gen2 function,
// sharing the same verbatim-policy store as v1 (CloudEmu does not enforce IAM;
// the policy round-trips so terraform google_cloudfunctions2_function_iam_member
// works).
func (h *Handler) serveV2IamPolicy(w http.ResponseWriter, r *http.Request, p v2Path) {
	key := p.fullName()
	if !h.gen2Exists(key) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	switch p.action {
	case actionGetIamPolicy:
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "getIamPolicy requires GET")
			return
		}

		h.writeIamPolicy(w, r, key)
	case actionSetIamPolicy:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "setIamPolicy requires POST")
			return
		}

		h.storeIamPolicy(w, r, key)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

// serveV2TestIamPermissions echoes the requested permissions for a gen2 function.
func (h *Handler) serveV2TestIamPermissions(w http.ResponseWriter, r *http.Request, p v2Path) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "testIamPermissions requires POST")
		return
	}

	if !h.gen2Exists(p.fullName()) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	var body testIamPermissionsRequest
	if !decodeJSON(w, r, &body, nil) {
		return
	}

	writeJSON(w, http.StatusOK, testIamPermissionsResponse(body))
}
