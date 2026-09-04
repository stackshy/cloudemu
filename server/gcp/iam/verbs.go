package iam

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	disableVerb = "disable"
	enableVerb  = "enable"

	// keyDisableReasonUserInitiated mirrors the real GCP enum value stamped on
	// a key disabled via the disable() method (as opposed to automatic
	// rotation-driven disablement, which cloudemu does not model).
	keyDisableReasonUserInitiated = "SERVICE_ACCOUNT_KEY_DISABLE_REASON_USER_INITIATED"
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

	// undelete acts on a tombstone, not a live account, so it must not go
	// through the existence check below (the SA is gone from the driver).
	if rt.verb == undeleteVerb {
		h.undeleteServiceAccount(w, r, rt.project, rt.name)

		return
	}

	// Confirm the SA exists (all remaining verbs act on an existing account).
	if _, err := h.iam.GetUser(r.Context(), rt.name); err != nil {
		writeCErr(w, err)

		return
	}

	h.dispatchSAVerb(w, r, rt)
}

// dispatchSAVerb routes the ":method" calls that act on an existing SA.
func (h *Handler) dispatchSAVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	switch rt.verb {
	case "getIamPolicy":
		h.getSAIamPolicy(w, rt.name)
	case "setIamPolicy":
		h.setSAIamPolicy(w, r, rt.name)
	case "testIamPermissions":
		h.testSAIamPermissions(w, r, rt.name)
	case "signBlob":
		h.signBlob(w, r, rt.name)
	case "signJwt":
		h.signJwt(w, r, rt.name)
	case "generateAccessToken":
		h.generateAccessToken(w, r)
	case enableVerb:
		h.setDisabled(w, rt.name, false)
	case disableVerb:
		h.setDisabled(w, rt.name, true)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown method: "+rt.verb)
	}
}

// testSAIamPermissions returns the subset of the requested permissions the
// caller holds. The emulator has no request-principal identity, so it grants
// exactly the permissions named in the SA's bound roles' includedPermissions
// (custom roles) plus any granted directly in the SA resource policy bindings.
// With no matching policy every requested permission is held — real GCP grants
// what the caller actually has; here the "caller" is the resource owner.
func (h *Handler) testSAIamPermissions(w http.ResponseWriter, r *http.Request, email string) {
	var body struct {
		Permissions []string `json:"permissions"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	held := h.heldPermissions(r, email)

	out := struct {
		Permissions []string `json:"permissions,omitempty"`
	}{Permissions: make([]string, 0, len(body.Permissions))}

	for _, p := range body.Permissions {
		if held == nil || held[p] {
			out.Permissions = append(out.Permissions, p)
		}
	}

	if len(out.Permissions) == 0 {
		out.Permissions = nil
	}

	writeJSON(w, &out)
}

// heldPermissions returns the set of permissions granted on the SA. A nil map
// means "grant everything requested" (no policy has been set, so the resource
// owner implicitly holds all permissions). A non-nil map is the explicit
// allow-set drawn from the SA's policy bindings' custom-role permissions.
func (h *Handler) heldPermissions(r *http.Request, email string) map[string]bool {
	h.mu.RLock()
	pol := h.saPolicy[email]
	h.mu.RUnlock()

	if pol == nil || len(pol.Bindings) == 0 {
		return nil
	}

	held := make(map[string]bool)

	for i := range pol.Bindings {
		roleName := pol.Bindings[i].Role
		// Custom roles are "projects/{p}/roles/{id}" — resolve their perms.
		id := roleName[strings.LastIndex(roleName, "/")+1:]

		dr, err := h.iam.GetRole(r.Context(), id)
		if err != nil {
			continue
		}

		props, perr := decodeRoleProps(dr.AssumeRolePolicyDoc)
		if perr != nil {
			continue
		}

		for _, p := range props.IncludedPermissions {
			held[p] = true
		}
	}

	return held
}

// undeleteServiceAccount restores a recently-deleted SA from its tombstone.
// The name segment may be the SA email or its 21-digit uniqueId, matching the
// two resource-name forms real GCP accepts.
func (h *Handler) undeleteServiceAccount(w http.ResponseWriter, r *http.Request, project, name string) {
	h.mu.Lock()

	email, tomb := h.takeTombstone(name)
	if tomb == nil {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"service account "+name+" was not found or is not recoverable")

		return
	}

	if tomb.disabled {
		h.disabled[email] = true
	}

	h.mu.Unlock()

	restoreProject := project
	if restoreProject == "-" {
		restoreProject = tomb.project
	}

	if _, err := h.iam.CreateUser(r.Context(), iamdriver.UserConfig{
		Name: email,
		Path: tomb.project,
		Tags: map[string]string{
			"displayName": tomb.displayName,
			"description": tomb.description,
		},
	}); err != nil {
		writeCErr(w, err)

		return
	}

	sa := serviceAccount{DisplayName: tomb.displayName, Description: tomb.description, Disabled: tomb.disabled}
	restored := toServiceAccountJSON(restoreProject, email, &sa)

	writeJSON(w, map[string]any{"restoredAccount": &restored})
}

// takeTombstone finds and removes the tombstone for name, which may be the SA
// email or its 21-digit uniqueId (the two resource-name forms real GCP accepts).
// The caller must hold h.mu.
func (h *Handler) takeTombstone(name string) (string, *deletedSA) {
	if tomb := h.deletedSA[name]; tomb != nil {
		delete(h.deletedSA, name)

		return name, tomb
	}

	for email, tomb := range h.deletedSA {
		if saUniqueID(email) == name {
			delete(h.deletedSA, email)

			return email, tomb
		}
	}

	return "", nil
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

// setSAIamPolicy enforces optimistic concurrency. If a policy already exists,
// the request's policy.etag must match the stored etag or the write is
// rejected with 409 ABORTED — mirroring real GCP's read-modify-write contract.
// Each accepted write bumps a per-SA version so successive states get distinct
// etags (the old base64(email+bindingCount) scheme collided across states).
func (h *Handler) setSAIamPolicy(w http.ResponseWriter, r *http.Request, email string) {
	var body struct {
		Policy iamPolicy `json:"policy"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	h.mu.Lock()

	if cur := h.saPolicy[email]; cur != nil && body.Policy.Etag != cur.Etag {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, "ABORTED",
			"there were concurrent policy changes; please retry the whole "+
				"read-modify-write with the new etag")

		return
	}

	pol := body.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	h.policyVersion[email]++
	pol.Etag = etagFor(email, h.policyVersion[email])
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

// routeKeyVerb dispatches the one-off ":method" service-account-key calls
// (disable, enable). Both are POSTs on a specific key, mirroring
// projects.serviceAccounts.keys.disable/enable in the real API.
func (h *Handler) routeKeyVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
			"method "+rt.verb+" requires POST")

		return
	}

	switch rt.verb {
	case disableVerb:
		h.setKeyDisabled(w, r, rt.name, rt.subName, true)
	case enableVerb:
		h.setKeyDisabled(w, r, rt.name, rt.subName, false)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown method: "+rt.verb)
	}
}

// setKeyDisabled toggles the disabled bit on an existing SA key, matching
// real GCP's projects.serviceAccounts.keys.disable/enable, which return an
// empty body on success. The driver has no update path for access keys, so
// the bit is tracked here (like the SA-level disabled bit) rather than
// mutated in the driver store.
func (h *Handler) setKeyDisabled(w http.ResponseWriter, r *http.Request, email, keyID string, disabled bool) {
	keys, err := h.iam.ListAccessKeys(r.Context(), email)
	if err != nil {
		writeCErr(w, err)
		return
	}

	found := false

	for i := range keys {
		if keys[i].AccessKeyID == keyID {
			found = true
			break
		}
	}

	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND",
			"service account key "+keyID+" not found")

		return
	}

	h.mu.Lock()
	h.keyDisabled[keyID] = disabled
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
