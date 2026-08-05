package iam

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// iamPolicy is the GCP IAM Policy resource returned by getIamPolicy /
// setIamPolicy. Bindings are stored verbatim so a set/get round-trips.
type iamPolicy struct {
	Version  int             `json:"version,omitempty"`
	Bindings []policyBinding `json:"bindings,omitempty"`
	Etag     string          `json:"etag,omitempty"`
}

type policyBinding struct {
	Role    string   `json:"role"`
	Members []string `json:"members,omitempty"`
}

// routeSAVerb dispatches the one-off ":method" service-account calls. All are
// POSTs on a specific SA.
func (h *Handler) routeSAVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
			"method "+rt.verb+" requires POST")

		return
	}

	// Confirm the SA exists (all these verbs act on an existing account).
	if _, err := h.iam.GetUser(r.Context(), rt.name); err != nil {
		writeCErr(w, err)
		return
	}

	switch rt.verb {
	case "getIamPolicy":
		h.getSAIamPolicy(w, rt.name)
	case "setIamPolicy":
		h.setSAIamPolicy(w, r, rt.name)
	case "signBlob":
		h.signBlob(w, r, rt.name)
	case "signJwt":
		h.signJwt(w, r, rt.name)
	case "generateAccessToken":
		h.generateAccessToken(w, r)
	case "enable":
		h.setDisabled(w, rt.name, false)
	case "disable":
		h.setDisabled(w, rt.name, true)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown method: "+rt.verb)
	}
}

func (h *Handler) getSAIamPolicy(w http.ResponseWriter, email string) {
	h.mu.RLock()
	pol := h.saPolicy[email]
	h.mu.RUnlock()

	if pol == nil {
		// An SA with no policy yet returns an empty, versioned policy (matching
		// real GCP, which never 404s getIamPolicy on an existing resource).
		pol = &iamPolicy{Version: 1, Etag: etagFor(email, 0)}
	}

	writeJSON(w, pol)
}

func (h *Handler) setSAIamPolicy(w http.ResponseWriter, r *http.Request, email string) {
	var body struct {
		Policy iamPolicy `json:"policy"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	pol := body.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	pol.Etag = etagFor(email, len(pol.Bindings))

	h.mu.Lock()
	h.saPolicy[email] = &pol
	h.mu.Unlock()

	writeJSON(w, &pol)
}

func (*Handler) signBlob(w http.ResponseWriter, r *http.Request, email string) {
	// The iam.googleapis.com signBlob uses bytesToSign/signature (base64).
	var body struct {
		BytesToSign string `json:"bytesToSign"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	// Deterministic non-cryptographic "signature": a hash of the SA + payload.
	// Real clients only need a stable, base64 blob back.
	sig := sha256.Sum256([]byte(email + ":" + body.BytesToSign))

	writeJSON(w, map[string]string{
		"keyId":     "key-" + email,
		"signature": base64.StdEncoding.EncodeToString(sig[:]),
	})
}

func (*Handler) signJwt(w http.ResponseWriter, r *http.Request, email string) {
	var body struct {
		Payload string `json:"payload"` // JSON claims string
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	// A JWT-shaped (header.payload.signature) string; not cryptographically
	// valid, but structurally what clients expect to parse.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(body.Payload))
	sig := sha256.Sum256([]byte(email + body.Payload))
	sigStr := base64.RawURLEncoding.EncodeToString(sig[:])

	writeJSON(w, map[string]string{
		"keyId":     "key-" + email,
		"signedJwt": header + "." + claims + "." + sigStr,
	})
}

func (*Handler) generateAccessToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope    []string `json:"scope"`
		Lifetime string   `json:"lifetime"`
	}

	_ = decodeJSON(w, r, &body) // request fields are optional for the stub

	expire := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	writeJSON(w, map[string]string{
		"accessToken": "ya29.emulated-" + strconv.FormatInt(int64(len(body.Scope)), 10),
		"expireTime":  expire,
	})
}

func (h *Handler) setDisabled(w http.ResponseWriter, email string, disabled bool) {
	h.mu.Lock()
	h.disabled[email] = disabled
	h.mu.Unlock()

	writeJSON(w, map[string]any{})
}

// etagFor returns a stable etag for a policy state.
func etagFor(email string, n int) string {
	return base64.StdEncoding.EncodeToString([]byte(email + ":" + strconv.Itoa(n)))
}

// decodeJSON decodes a JSON request body, writing a 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalidArgument", "invalid JSON: "+err.Error())
		return false
	}

	return true
}
