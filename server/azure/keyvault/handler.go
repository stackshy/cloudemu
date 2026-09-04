// Package keyvault implements the Azure Key Vault secrets data-plane API
// (/secrets/…, /deletedsecrets/…) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets clients
// pointed at this server set, read, update, list, delete, recover, back up and
// restore secrets against the shared secrets driver.
//
// Key Vault uses challenge-based auth: the SDK's first request carries no
// Authorization header and expects a 401 with a WWW-Authenticate challenge
// before retrying with a bearer token. The handler serves that challenge and
// accepts any token. Point clients at the server with
// DisableChallengeResourceVerification since the emulated vault's host is not
// under vault.azure.net.
//
// Coverage (Key Vault 7.x REST shapes):
//
//	PUT    /secrets/{name}               — set secret (create or new version)
//	GET    /secrets/{name}[/{version}]   — get current or specific version
//	PATCH  /secrets/{name}/{version}     — update version attributes/tags
//	GET    /secrets/{name}/versions      — list versions
//	GET    /secrets                      — list secrets
//	DELETE /secrets/{name}               — soft-delete secret
//	POST   /secrets/{name}/backup        — backup secret
//	POST   /secrets/restore              — restore secret
//	GET    /deletedsecrets               — list deleted secrets
//	GET    /deletedsecrets/{name}        — get deleted secret
//	POST   /deletedsecrets/{name}/recover— recover deleted secret
//	DELETE /deletedsecrets/{name}        — purge deleted secret
package keyvault

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	pathPrefix    = "/secrets"
	deletedPrefix = "/deletedsecrets"
	versionsSeg   = "versions"
	backupSeg     = "backup"
	restoreSeg    = "restore"
	recoverSeg    = "recover"
)

// Handler serves the Key Vault secrets data-plane API against a Secrets driver
// that also implements the Azure-specific KeyVaultSecrets surface.
type Handler struct {
	kv secretsdriver.KeyVaultSecrets
}

// New returns a Key Vault handler backed by s. s must implement the
// Azure-specific KeyVaultSecrets surface (the Azure provider mock does); a
// backend that does not is served 500 on every data-plane call.
func New(s secretsdriver.Secrets) *Handler {
	kv, _ := s.(secretsdriver.KeyVaultSecrets)

	return &Handler{kv: kv}
}

// Matches claims /secrets and /deletedsecrets data-plane requests. Disjoint
// from ARM (/subscriptions/…) and the Databricks secrets API (/api/{ver}/…).
func (*Handler) Matches(r *http.Request) bool {
	_, p, ok := vaultScope(r)
	if !ok {
		return false
	}

	return p == pathPrefix || strings.HasPrefix(p, pathPrefix+"/") ||
		p == deletedPrefix || strings.HasPrefix(p, deletedPrefix+"/")
}

// ServeHTTP answers the bearer challenge for unauthenticated requests, then
// routes on the vault-stripped path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, kvPath, _ := vaultScope(r)

	serveDataPlane(w, r, kvPath, h.kv == nil, "Key Vault secrets backend unavailable", dataPlaneRoutes{
		deletedPrefix: deletedPrefix,
		mainPrefix:    pathPrefix,
		routeDeleted:  func(tail string) { h.routeDeleted(w, r, tail) },
		routeMain:     func(tail string) { h.routeSecrets(w, r, tail) },
	})
}

// dataPlaneRoutes describes how a Key Vault data-plane handler splits its two
// path spaces (the live surface and its /deleted… counterpart).
type dataPlaneRoutes struct {
	deletedPrefix string
	mainPrefix    string
	routeDeleted  func(tail string)
	routeMain     func(tail string)
}

// serveDataPlane runs the shared Key Vault data-plane preamble — bearer
// challenge, backend-availability check, then dispatch to the deleted or main
// path space — used by both the secrets and keys handlers.
// path is the vault-stripped Key Vault data-plane path (from vaultScope), so a
// bare-host /{vault}/secrets/… routes identically to a vault-host /secrets/….
func serveDataPlane(
	w http.ResponseWriter, r *http.Request, path string, backendUnavailable bool, unavailableMsg string, routes dataPlaneRoutes,
) {
	if bearerChallenge(w, r) {
		return
	}

	if backendUnavailable {
		writeErr(w, http.StatusInternalServerError, "InternalServerError", unavailableMsg)
		return
	}

	if path == routes.deletedPrefix || strings.HasPrefix(path, routes.deletedPrefix+"/") {
		routes.routeDeleted(strings.Trim(strings.TrimPrefix(path, routes.deletedPrefix), "/"))
		return
	}

	routes.routeMain(strings.Trim(strings.TrimPrefix(path, routes.mainPrefix), "/"))
}

// routeSecrets dispatches /secrets[...] requests.
func (h *Handler) routeSecrets(w http.ResponseWriter, r *http.Request, tail string) {
	switch {
	case tail == "" && r.Method == http.MethodGet:
		h.listSecrets(w, r)
	case tail == restoreSeg && r.Method == http.MethodPost:
		h.restoreSecret(w, r)
	case tail == "":
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	default:
		h.routeNamedSecret(w, r, tail)
	}
}

// routeNamedSecret dispatches /secrets/{name}[/{version}|/versions|/backup].
func (h *Handler) routeNamedSecret(w http.ResponseWriter, r *http.Request, tail string) {
	name, sub, hasSub := strings.Cut(tail, "/")
	if !hasSub {
		h.routeBareSecret(w, r, name)
		return
	}

	switch {
	case sub == versionsSeg && r.Method == http.MethodGet:
		h.listSecretVersions(w, r, name)
	case sub == backupSeg && r.Method == http.MethodPost:
		h.backupSecret(w, r, name)
	case strings.Contains(sub, "/"):
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	case r.Method == http.MethodGet:
		h.getSecret(w, r, name, sub)
	case r.Method == http.MethodPatch:
		h.updateSecret(w, r, name, sub)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeBareSecret dispatches /secrets/{name} requests by method.
func (h *Handler) routeBareSecret(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPut:
		h.setSecret(w, r, name)
	case http.MethodGet:
		h.getSecret(w, r, name, "")
	case http.MethodDelete:
		h.deleteSecret(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeDeleted dispatches /deletedsecrets[...] requests.
//
//nolint:dupl // parallel soft-delete router for secrets vs keys; the shared shape is intentional
func (h *Handler) routeDeleted(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			h.listDeletedSecrets(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")

		return
	}

	name, sub, hasSub := strings.Cut(tail, "/")

	switch {
	case !hasSub && r.Method == http.MethodGet:
		h.getDeletedSecret(w, r, name)
	case !hasSub && r.Method == http.MethodDelete:
		h.purgeDeletedSecret(w, r, name)
	case sub == recoverSeg && r.Method == http.MethodPost:
		h.recoverDeletedSecret(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// vaultSuffixes are the DNS suffixes real Key Vault and Managed HSM data-plane
// hosts are addressed under, across Azure public/US Gov/China clouds. The
// vault (or HSM) name is the leading label of the host.
//
//nolint:gochecknoglobals // read-only lookup table, not mutable state
var vaultSuffixes = []string{
	".vault.azure.net",
	".vault.azure.cn",
	".vault.usgovcloudapi.net",
	".managedhsm.azure.net",
	".managedhsm.azure.cn",
	".managedhsm.usgovcloudapi.net",
}

// kvDataPlaneKeywords are the leading path segments of the Key Vault data-plane
// surface. A request is a Key Vault data-plane request only when its
// vault-stripped path begins with one of these.
//
//nolint:gochecknoglobals // read-only lookup set, not mutable state
var kvDataPlaneKeywords = map[string]bool{
	"secrets": true, "deletedsecrets": true,
	"keys": true, "deletedkeys": true,
	"certificates": true, "deletedcertificates": true,
}

// vaultScope resolves the vault that scopes isolation for r and the Key Vault
// data-plane path with any vault-selecting prefix stripped, reporting whether r
// is a Key Vault data-plane request at all. Two addressing forms are supported:
//
//   - Vault host: r.Host carries a {vault}.vault.azure.net (or Managed HSM / Gov
//     / China) suffix — the real-cloud form. The vault is the host's leading
//     label and the path is unchanged. On this form Key Vault always wins, so it
//     never shadows a blob container of the same name.
//   - Bare host (a local `serve` on localhost:PORT): the vault is the leading
//     path segment, i.e. /{vault}/secrets/…, so multiple vaults isolate. A bare
//     /secrets (no vault segment) is NOT a Key Vault request and falls through to
//     blob storage — which is what lets a blob container literally named
//     "secrets"/"keys"/"certificates" be created.
func vaultScope(r *http.Request) (vault, kvPath string, ok bool) {
	path := r.URL.Path

	host, _, _ := strings.Cut(r.Host, ":")
	for _, suffix := range vaultSuffixes {
		if i := strings.Index(host, suffix); i > 0 {
			if !kvDataPlaneKeywords[firstSegment(path)] {
				return "", "", false
			}

			return host[:i], path, true
		}
	}

	// Bare host: /{vault}/{keyword}/… — the vault is the leading segment. A bare
	// /{keyword} (no vault) or a reserved leading segment is not a KV request.
	seg, rest, hasRest := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !hasRest || seg == "" || seg == "subscriptions" || kvDataPlaneKeywords[seg] {
		return "", "", false
	}

	if !kvDataPlaneKeywords[firstSegment("/"+rest)] {
		return "", "", false
	}

	return seg, "/" + rest, true
}

// firstSegment returns the first '/'-separated segment of path.
func firstSegment(path string) string {
	seg, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")

	return seg
}

// vaultFromRequest returns the vault name that scopes secret/key/certificate
// isolation for r, so a secret created in one vault is never visible through
// another. It delegates to vaultScope; operation handlers call it only after
// Matches has confirmed the request is a Key Vault data-plane request, so the
// scope always resolves. The "default" fallback is unreachable in that path and
// kept only as a defensive value.
func vaultFromRequest(r *http.Request) string {
	if v, _, ok := vaultScope(r); ok {
		return v
	}

	return "default"
}

// bearerChallenge answers an unauthenticated request with the Key Vault bearer
// challenge and reports whether it handled the request. Key Vault SDKs expect a
// 401 with a WWW-Authenticate header before retrying with a token.
func bearerChallenge(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return false
	}

	w.Header().Set("WWW-Authenticate",
		`Bearer authorization="https://login.microsoftonline.com/common", resource="https://vault.azure.net"`)
	writeErr(w, http.StatusUnauthorized, "Unauthorized", "bearer token required")

	return true
}

// writeErr emits a Key Vault-style error body with the given HTTP status.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

// writeCErr maps a canonical cloudemu error to a Key Vault error response.
func writeCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "SecretNotFound", err.Error())
	case cerrors.IsAlreadyExists(err) && strings.Contains(err.Error(), "deleted but recoverable"):
		// Real Key Vault answers a set/create against a soft-deleted name with
		// 409 Conflict and inner error code ObjectIsDeletedButRecoverable.
		writeErrInner(w, http.StatusConflict, "Conflict", err.Error(), "ObjectIsDeletedButRecoverable")
	case cerrors.IsAlreadyExists(err):
		writeErr(w, http.StatusConflict, "Conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeErr(w, http.StatusBadRequest, "BadParameter", err.Error())
	case cerrors.IsPermissionDenied(err):
		// A disabled, not-yet-valid or expired secret version: real Key Vault
		// answers get with 403 Forbidden rather than falling back to an older
		// usable version.
		writeErr(w, http.StatusForbidden, "Forbidden", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}

// writeErrInner emits a Key Vault-style error body carrying a nested inner
// error code, matching the shape real vaults return for recoverable conflicts.
func writeErrInner(w http.ResponseWriter, status int, code, msg, innerCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    msg,
			"innererror": map[string]string{"code": innerCode},
		},
	})
}
