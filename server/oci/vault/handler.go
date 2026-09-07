// Package vault implements OCI Vault's REST API against a CloudEmu secrets
// driver. OCI splits the service across two API versions and CloudEmu claims
// both, because one HTTP server stands in for every OCI endpoint:
//
//	/20180608 — KMS management and Vault secret management
//	/20190301 — the secret-retrieval data plane
//
// Coverage under /20180608:
//
//	POST/GET             /vaults                              — CreateVault, ListVaults
//	GET/PUT              /vaults/{vaultId}                    — GetVault, UpdateVault
//	POST                 /vaults/{vaultId}/actions/{scheduleDeletion,cancelDeletion,changeCompartment}
//	POST/GET             /keys                                — CreateKey, ListKeys
//	GET/PUT              /keys/{keyId}                        — GetKey, UpdateKey
//	POST                 /keys/{keyId}/actions/{scheduleDeletion,cancelDeletion,changeCompartment}
//	POST/GET             /keys/{keyId}/keyVersions            — CreateKeyVersion (rotation), ListKeyVersions
//	GET                  /keys/{keyId}/keyVersions/{id}       — GetKeyVersion
//	POST/GET             /secrets                             — CreateSecret, ListSecrets
//	GET/PUT              /secrets/{secretId}                  — GetSecret, UpdateSecret
//	GET                  /secrets/actions/getByName           — GetSecretByName
//	POST                 /secrets/{secretId}/actions/{scheduleDeletion,cancelDeletion,changeCompartment}
//	GET                  /secrets/{secretId}/versions         — ListSecretVersions
//	GET                  /secrets/{secretId}/versions/{n}     — GetSecretVersion
//	POST                 /secrets/{secretId}/versions/{n}/actions/{scheduleDeletion,cancelDeletion}
//
// Coverage under /20190301:
//
//	GET /secretbundles/{secretId}            — GetSecretBundle, by versionNumber, secretVersionName or stage
//	GET /secretbundles/{secretId}/versions   — ListSecretBundleVersions
//	GET /secretbundles/actions/getByName     — GetSecretBundleByName
//
// The KMS crypto endpoint — encrypt, decrypt, sign, verify and
// generateDataEncryptionKey — shares the /20180608 prefix. CloudEmu stores no
// key material, so those paths are claimed only to answer 501 naming the gap
// rather than leaving a caller with a bare 404.
//
// Deletion is scheduled, never immediate: a vault, key, secret or secret
// version moves to PENDING_DELETION and stays there, since nothing reaps it,
// until the deletion is canceled.
package vault

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// The two API versions OCI Vault is published under.
const (
	apiVersionManagement = "20180608"
	apiVersionBundles    = "20190301"
)

// Collections this handler claims.
const (
	segVaults        = "vaults"
	segKeys          = "keys"
	segSecrets       = "secrets"
	segSecretBundles = "secretbundles"
)

// Sub-collections and the action shape /{collection}/{id}/actions/{action}.
const (
	segKeyVersions = "keyVersions"
	segVersions    = "versions"
	segActions     = "actions"
)

// Crypto endpoint operations, claimed only to report them as unemulated.
const (
	segEncrypt   = "encrypt"
	segDecrypt   = "decrypt"
	segGenerate  = "generateDataEncryptionKey"
	segSign      = "sign"
	segVerify    = "verify"
	segExportKey = "exportKey"
)

// Actions.
const (
	actionScheduleDeletion  = "scheduleDeletion"
	actionCancelDeletion    = "cancelDeletion"
	actionChangeCompartment = "changeCompartment"
	actionGetByName         = "getByName"
)

// Error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
)

// maxPathSegments is /{version}/secrets/{id}/versions/{n}/actions/{action}.
const maxPathSegments = 7

// Path shapes, as segment counts after the API version.
const (
	lenCollection = 1
	lenResource   = 2
	lenSub        = 3
	lenSubID      = 4
	lenSubAction  = 6
)

// Segment positions after the API version, for the shapes above.
const (
	idxCollection = 0
	idxID         = 1
	idxSub        = 2
	idxSubID      = 3
	idxSubActions = 4
	idxSubAction  = 5
)

// Handler serves OCI Vault against a secrets driver.
type Handler struct {
	extras Extras
	work   *workrequest.Store
}

// New returns a Vault handler. The portable driver is taken so registration
// matches every other service, but OCI addresses vaults, keys and secrets in
// ways its seven operations cannot express, so the handler serves entirely
// through Extras and answers 501 when the driver does not satisfy it. work
// records the mutations real OCI runs asynchronously; a nil store leaves the
// compartment moves unserved and the rest unstamped.
func New(s secretsdriver.Secrets, work *workrequest.Store) *Handler {
	extras, _ := s.(Extras)

	return &Handler{extras: extras, work: work}
}

// route is a parsed Vault path: the API version, and the segments after it.
type route struct {
	Version  string
	Segments []string
}

// seg returns the i-th segment after the API version, or the empty string.
func (rt route) seg(i int) string {
	if i >= len(rt.Segments) {
		return ""
	}

	return rt.Segments[i]
}

// count is the number of segments after the API version.
func (rt route) count() int {
	return len(rt.Segments)
}

// Matches claims the Vault collections under /20180608 and the secret bundles
// under /20190301, and nothing else sharing either prefix.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	if rt.Version == apiVersionBundles {
		return rt.seg(idxCollection) == segSecretBundles
	}

	switch rt.seg(idxCollection) {
	case segVaults, segKeys, segSecrets,
		segEncrypt, segDecrypt, segGenerate, segSign, segVerify, segExportKey:
		return true
	}

	return false
}

// ServeHTTP routes on API version, then on collection.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed vault path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired secrets driver does not implement OCI Vault")

		return
	}

	if rt.Version == apiVersionBundles {
		h.serveBundles(w, r, rt)
		return
	}

	switch rt.seg(idxCollection) {
	case segVaults:
		h.serveVaults(w, r, rt)
	case segKeys:
		h.serveKeys(w, r, rt)
	case segSecrets:
		h.serveSecrets(w, r, rt)
	default:
		unemulatedCrypto(w, r, rt.seg(idxCollection))
	}
}

// unemulatedCrypto reports a crypto endpoint operation. CloudEmu stores no key
// material — a master encryption key here is a record, not a cipher — so an
// encrypt or sign call would have to invent a ciphertext.
func unemulatedCrypto(w http.ResponseWriter, r *http.Request, operation string) {
	ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
		operation+" is not emulated: CloudEmu records master encryption keys but stores no key material")
}

// parsePath splits /{version}/{segments…}.
func parsePath(urlPath string) (route, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || len(parts) > maxPathSegments {
		return route{}, false
	}

	if parts[0] != apiVersionManagement && parts[0] != apiVersionBundles {
		return route{}, false
	}

	for _, p := range parts {
		if p == "" {
			return route{}, false
		}
	}

	return route{Version: parts[0], Segments: parts[1:]}, true
}

// isAction reports whether rt addresses /{collection}/{id}/actions/{action}.
func isAction(rt route) bool {
	return rt.count() == lenSubID && rt.seg(idxSub) == segActions
}

// methodNotAllowed is the response for a verb a collection does not serve.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed")
}

// notFound reports a path shape the handler claims but does not serve.
func notFound(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "no such vault resource")
}

// unknownAction reports an action a collection does not define.
func unknownAction(w http.ResponseWriter, r *http.Request, action string) {
	ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+action)
}

// accept records a work request for a mutation real OCI runs asynchronously
// and stamps the header an SDK waiter polls on.
func (h *Handler) accept(w http.ResponseWriter, operation, compartmentID, entityType, actionType, id string) {
	if h.work == nil {
		return
	}

	wrID := h.work.Accept(operation, compartmentID, workrequest.Resource{
		EntityType: entityType,
		ActionType: actionType,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
}

// decodeDeletion reads a scheduleDeletion body, which OCI allows to be absent.
func decodeDeletion(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.ContentLength == 0 {
		return "", true
	}

	var req deletionRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return "", false
	}

	return req.TimeOfDeletion, true
}

// changeCompartment moves a resource between compartments. OCI runs it
// asynchronously and answers with nothing but the work request a waiter polls.
func (h *Handler) changeCompartment(
	w http.ResponseWriter, r *http.Request, id, operation, entity string,
	move func(id, compartmentID string) error,
) {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	compartmentID, ok := decodeCompartmentMove(w, r)
	if !ok {
		return
	}

	if err := move(id, compartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, operation, compartmentID, entity, workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// decodeCompartmentMove reads a changeCompartment body.
func decodeCompartmentMove(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return "", false
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return "", false
	}

	return req.CompartmentID, true
}

// rejectDefinedTags refuses a request carrying defined tags, which CloudEmu
// does not model. Accepting and dropping them would leave a caller believing a
// tag namespace had been applied.
func rejectDefinedTags(w http.ResponseWriter, r *http.Request, tags definedTags) bool {
	if len(tags) == 0 {
		return true
	}

	ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
		"definedTags is not emulated: CloudEmu models no tag namespaces, use freeformTags")

	return false
}

// rejectUnmodelled refuses a request naming a field the emulator has no
// behavior for, naming the field rather than dropping it.
func rejectUnmodelled(w http.ResponseWriter, r *http.Request, field string, given bool) bool {
	if !given {
		return true
	}

	ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, field+" is not emulated")

	return false
}

// paginate applies OCI's limit and opaque page cursor, stamping the cursor for
// the next page. The cursor is the offset the next page starts at.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	start := 0

	if token := ocirest.Page(r); token != "" {
		if n, err := strconv.Atoi(token); err == nil && n > 0 {
			start = n
		}
	}

	// items[:0] rather than nil: an empty page is [] on the wire, not null.
	if start >= len(items) {
		return items[:0]
	}

	end := min(start+ocirest.Limit(r), len(items))
	if end < len(items) {
		ocirest.SetNextPage(w, strconv.Itoa(end))
	}

	return items[start:end]
}

// writeList renders a driver listing as a page of wire shapes.
func writeList[T, R any](w http.ResponseWriter, r *http.Request, items []T, render func(*T) R) {
	out := make([]R, 0, len(items))
	for i := range items {
		out = append(out, render(&items[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

// versionNumber parses a secret version number out of the path.
func versionNumber(w http.ResponseWriter, r *http.Request, raw string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"secretVersionNumber "+raw+" is not a version number")

		return 0, false
	}

	return n, true
}
