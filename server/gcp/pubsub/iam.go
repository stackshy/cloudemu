package pubsub

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

// ---------- IAM ----------
//
// Pub/Sub topics and subscriptions expose the standard google.iam.v1 policy
// surface (getIamPolicy/setIamPolicy/testIamPermissions), which
// google_pubsub_topic_iam_* / google_pubsub_subscription_iam_* Terraform
// resources depend on. Policies are stored per-resource and round-trip verbatim.

func (h *Handler) serveIam(w http.ResponseWriter, r *http.Request, resType, name, action string) {
	switch action {
	case verbGetIamPolicy:
		h.getIamPolicy(w, r, resType, name)
	case verbSetIamPolicy:
		h.setIamPolicy(w, r, resType, name)
	case verbTestIamPermissions:
		testIamPermissions(w, r)
	default:
		writeError(w, http.StatusNotFound, reasonNotFound, "unknown IAM verb: "+action)
	}
}

func (h *Handler) getIamPolicy(w http.ResponseWriter, r *http.Request, resType, name string) {
	if !h.resourceExists(r, resType, name) {
		writeError(w, http.StatusNotFound, reasonNotFound, resType+" "+name+" not found")
		return
	}

	h.mu.RLock()
	pol := h.loadPolicy(resType, name)
	h.mu.RUnlock()

	if pol == nil {
		pol = &iamPolicy{Version: 1, Etag: h.nextEtag()}
	}

	writeJSON(w, http.StatusOK, pol)
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, resType, name string) {
	if !h.resourceExists(r, resType, name) {
		writeError(w, http.StatusNotFound, reasonNotFound, resType+" "+name+" not found")
		return
	}

	var req setIamPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	pol := req.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	pol.Etag = h.nextEtag()

	h.mu.Lock()
	h.storePolicy(resType, name, &pol)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, pol)
}

func testIamPermissions(w http.ResponseWriter, r *http.Request) {
	var req testIamPermissionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// The emulator grants every requested permission (no policy enforcement on
	// the wire), so echo the request back as the held subset.
	writeJSON(w, http.StatusOK, testIamPermissionsResponse(req))
}

func (h *Handler) resourceExists(r *http.Request, resType, name string) bool {
	switch resType {
	case resTopics:
		_, err := h.findQueueByName(r, name)
		return err == nil
	case resSubscriptions:
		h.mu.RLock()
		_, ok := h.subs[name]
		h.mu.RUnlock()

		return ok
	case resSnapshots:
		h.mu.RLock()
		_, ok := h.snapshots[name]
		h.mu.RUnlock()

		return ok
	default:
		return false
	}
}

// loadPolicy returns the stored policy for a resource, or nil. The caller holds h.mu.
func (h *Handler) loadPolicy(resType, name string) *iamPolicy {
	switch resType {
	case resTopics:
		if ts, ok := h.topics[name]; ok {
			return ts.iam
		}
	case resSubscriptions:
		if s, ok := h.subs[name]; ok {
			return s.iam
		}
	}

	return nil
}

// storePolicy persists a resource's policy. The caller holds h.mu.
func (h *Handler) storePolicy(resType, name string, pol *iamPolicy) {
	switch resType {
	case resTopics:
		h.topicLog(name).iam = pol
	case resSubscriptions:
		if s, ok := h.subs[name]; ok {
			s.iam = pol
		}
	}
}

func (h *Handler) nextEtag() string {
	return base64.StdEncoding.EncodeToString([]byte("etag-" + strconv.FormatUint(h.ackCounter.Add(1), 10)))
}
