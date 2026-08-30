// This file implements the Azure Key Vault keys data-plane API (/keys/…,
// /deletedkeys/…) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys clients
// pointed at this server create, import, read, list, update, delete, recover
// and purge keys, and run real cryptographic operations
// (encrypt/decrypt/wrapKey/unwrapKey/sign/verify) against the KeyVaultKeys
// surface of the secrets driver.
//
// Coverage (Key Vault 7.x REST shapes):
//
//	POST   /keys/{name}/create                 — create key (RSA/EC)
//	PUT    /keys/{name}                         — import key
//	GET    /keys/{name}[/{version}]             — get current or specific version
//	PATCH  /keys/{name}/{version}               — update version attributes
//	GET    /keys/{name}/versions                — list versions
//	GET    /keys                                — list keys
//	DELETE /keys/{name}                         — soft-delete key
//	POST   /keys/{name}/{version}/encrypt       — encrypt
//	POST   /keys/{name}/{version}/decrypt       — decrypt
//	POST   /keys/{name}/{version}/wrapkey       — wrap key
//	POST   /keys/{name}/{version}/unwrapkey     — unwrap key
//	POST   /keys/{name}/{version}/sign          — sign digest
//	POST   /keys/{name}/{version}/verify        — verify signature
//	GET    /keys/{name}/rotationpolicy          — get key rotation policy
//	PUT    /keys/{name}/rotationpolicy          — update key rotation policy
//	GET    /deletedkeys                         — list deleted keys
//	GET    /deletedkeys/{name}                  — get deleted key
//	POST   /deletedkeys/{name}/recover          — recover deleted key
//	DELETE /deletedkeys/{name}                  — purge deleted key
package keyvault

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	keysPrefix        = "/keys"
	deletedKeysPrefix = "/deletedkeys"
	createSeg         = "create"
	rotateSeg         = "rotate"
	rotationPolicySeg = "rotationpolicy"
	encryptSeg        = "encrypt"
	decryptSeg        = "decrypt"
	wrapSeg           = "wrapkey"
	unwrapSeg         = "unwrapkey"
	signSeg           = "sign"
	verifySeg         = "verify"
)

// KeysHandler serves the Key Vault keys data-plane API against a backend that
// implements the Azure-specific KeyVaultKeys surface.
type KeysHandler struct {
	kv secretsdriver.KeyVaultKeys
}

// NewKeys returns a keys handler backed by s. s must implement the
// Azure-specific KeyVaultKeys surface (the Azure provider mock does); a backend
// that does not is served 500 on every keys data-plane call.
func NewKeys(s secretsdriver.Secrets) *KeysHandler {
	kv, _ := s.(secretsdriver.KeyVaultKeys)

	return &KeysHandler{kv: kv}
}

// Matches claims /keys and /deletedkeys data-plane requests. Disjoint from ARM
// (/subscriptions/…) and from the secrets surface (/secrets, /deletedsecrets).
func (*KeysHandler) Matches(r *http.Request) bool {
	_, p, ok := vaultScope(r)
	if !ok {
		return false
	}

	return p == keysPrefix || strings.HasPrefix(p, keysPrefix+"/") ||
		p == deletedKeysPrefix || strings.HasPrefix(p, deletedKeysPrefix+"/")
}

// ServeHTTP answers the bearer challenge for unauthenticated requests, then
// routes on the vault-stripped path and method.
func (h *KeysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, kvPath, _ := vaultScope(r)

	serveDataPlane(w, r, kvPath, h.kv == nil, "Key Vault keys backend unavailable", dataPlaneRoutes{
		deletedPrefix: deletedKeysPrefix,
		mainPrefix:    keysPrefix,
		routeDeleted:  func(tail string) { h.routeDeleted(w, r, tail) },
		routeMain:     func(tail string) { h.routeKeys(w, r, tail) },
	})
}

func (h *KeysHandler) routeKeys(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			h.listKeys(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")

		return
	}

	name, sub, hasSub := strings.Cut(tail, "/")
	if !hasSub {
		h.routeBareKey(w, r, name)
		return
	}

	h.routeNamedKey(w, r, name, sub)
}

// routeBareKey dispatches /keys/{name} requests by method.
func (h *KeysHandler) routeBareKey(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.importKey(w, r, name)
	case http.MethodGet:
		h.getKey(w, r, name, "")
	case http.MethodDelete:
		h.deleteKey(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeNamedKey dispatches /keys/{name}/{sub}[/{op}] requests.
func (h *KeysHandler) routeNamedKey(w http.ResponseWriter, r *http.Request, name, sub string) {
	// Fixed named sub-resources (create, rotate, versions, rotationpolicy) are
	// matched first; rotationpolicy in particular must be routed before the
	// version/op split below, which would otherwise misread it as a version.
	if h.routeKeySubcommand(w, r, name, sub) {
		return
	}

	// A crypto operation on the current version collapses to /keys/{name}/{op}
	// (the SDK drops the empty version segment).
	if isCryptoOp(sub) {
		h.routeCryptoOp(w, r, name, "", sub)
		return
	}

	// Remaining shapes are version-scoped: /keys/{name}/{version}[/{op}].
	version, op, hasOp := strings.Cut(sub, "/")
	if hasOp {
		h.routeCryptoOp(w, r, name, version, op)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getKey(w, r, name, version)
	case http.MethodPatch:
		h.updateKey(w, r, name, version)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeKeySubcommand handles the fixed, non-version-scoped named sub-resources
// of a key. It returns true when it has handled (or rejected) the request; a
// false return means sub is a version (or version/op) to be routed by the
// caller.
func (h *KeysHandler) routeKeySubcommand(w http.ResponseWriter, r *http.Request, name, sub string) bool {
	switch {
	case sub == createSeg && r.Method == http.MethodPost:
		h.createKey(w, r, name)
	case sub == rotateSeg && r.Method == http.MethodPost:
		h.rotateKey(w, r, name)
	case sub == versionsSeg && r.Method == http.MethodGet:
		h.listKeyVersions(w, r, name)
	case sub == rotationPolicySeg:
		h.routeKeyRotationPolicy(w, r, name)
	default:
		return false
	}

	return true
}

func isCryptoOp(seg string) bool {
	switch seg {
	case encryptSeg, decryptSeg, wrapSeg, unwrapSeg, signSeg, verifySeg:
		return true
	default:
		return false
	}
}

// routeCryptoOp dispatches /keys/{name}/{version}/{op} crypto operations.
func (h *KeysHandler) routeCryptoOp(w http.ResponseWriter, r *http.Request, name, version, op string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
		return
	}

	switch op {
	case encryptSeg:
		h.encrypt(w, r, name, version)
	case decryptSeg:
		h.decrypt(w, r, name, version)
	case wrapSeg:
		h.wrapKey(w, r, name, version)
	case unwrapSeg:
		h.unwrapKey(w, r, name, version)
	case signSeg:
		h.sign(w, r, name, version)
	case verifySeg:
		h.verify(w, r, name, version)
	default:
		writeErr(w, http.StatusNotFound, "NotFound", "unsupported Key Vault operation")
	}
}

// routeKeyRotationPolicy dispatches GET/PUT /keys/{name}/rotationpolicy.
func (h *KeysHandler) routeKeyRotationPolicy(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getKeyRotationPolicy(w, r, name)
	case http.MethodPut:
		h.updateKeyRotationPolicy(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeDeleted dispatches /deletedkeys[...] requests. It mirrors the secrets
// handler's /deletedsecrets router (get/purge/recover) against the keys backend.
//
//nolint:dupl // parallel soft-delete router for keys vs secrets; the shared shape is intentional
func (h *KeysHandler) routeDeleted(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			h.listDeletedKeys(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")

		return
	}

	name, sub, hasSub := strings.Cut(tail, "/")

	switch {
	case !hasSub && r.Method == http.MethodGet:
		h.getDeletedKey(w, r, name)
	case !hasSub && r.Method == http.MethodDelete:
		h.purgeDeletedKey(w, r, name)
	case sub == recoverSeg && r.Method == http.MethodPost:
		h.recoverDeletedKey(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// writeKeyErr maps a canonical cloudemu error to a Key Vault key error response.
func writeKeyErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "KeyNotFound", err.Error())
	case cerrors.IsAlreadyExists(err) && strings.Contains(err.Error(), "deleted but recoverable"):
		writeErrInner(w, http.StatusConflict, "Conflict", err.Error(), "ObjectIsDeletedButRecoverable")
	case cerrors.IsAlreadyExists(err):
		writeErr(w, http.StatusConflict, "Conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeErr(w, http.StatusBadRequest, "BadParameter", err.Error())
	case cerrors.IsPermissionDenied(err):
		writeErr(w, http.StatusForbidden, "Forbidden", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeErr(w, http.StatusForbidden, "Forbidden", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}
