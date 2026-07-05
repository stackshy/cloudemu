// Package keyvault implements the Azure Key Vault secrets data-plane API
// (/secrets/…) as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets clients
// pointed at this server set, read, list, and delete secrets against the
// shared secrets driver.
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
//	PUT    /secrets/{name}            — set secret (create or new version)
//	GET    /secrets/{name}            — get current secret value
//	GET    /secrets/{name}/{version}  — get specific version
//	GET    /secrets/{name}/versions   — list versions
//	GET    /secrets                   — list secrets
//	DELETE /secrets/{name}            — delete secret
package keyvault

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
)

const pathPrefix = "/secrets"

const versionsSeg = "versions"

// Handler serves the Key Vault secrets data-plane API against a Secrets
// driver.
type Handler struct {
	secrets secretsdriver.Secrets
}

// New returns a Key Vault handler backed by s.
func New(s secretsdriver.Secrets) *Handler {
	return &Handler{secrets: s}
}

// Matches claims /secrets data-plane requests. Disjoint from ARM
// (/subscriptions/…) and from the Databricks secrets API (/api/{ver}/secrets)
// and registered before the blob storage REST fallback.
func (*Handler) Matches(r *http.Request) bool {
	return r.URL.Path == pathPrefix || strings.HasPrefix(r.URL.Path, pathPrefix+"/")
}

// ServeHTTP answers the bearer challenge for unauthenticated requests, then
// routes on the path tail and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" {
		w.Header().Set("WWW-Authenticate",
			`Bearer authorization="https://login.microsoftonline.com/common", resource="https://vault.azure.net"`)
		writeErr(w, http.StatusUnauthorized, "Unauthorized", "bearer token required")

		return
	}

	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, pathPrefix), "/")
	if tail == "" {
		if r.Method == http.MethodGet {
			h.listSecrets(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")

		return
	}

	h.routeSecret(w, r, tail)
}

// routeSecret dispatches /secrets/{name}[/{version}|/versions] requests.
func (h *Handler) routeSecret(w http.ResponseWriter, r *http.Request, tail string) {
	name, sub, hasSub := strings.Cut(tail, "/")
	if !hasSub {
		h.routeBareSecret(w, r, name)
		return
	}

	switch {
	case sub == versionsSeg && r.Method == http.MethodGet:
		h.listSecretVersions(w, r, name)
	case !strings.Contains(sub, "/") && r.Method == http.MethodGet:
		h.getSecret(w, r, name, sub)
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
	case cerrors.IsAlreadyExists(err):
		writeErr(w, http.StatusConflict, "Conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeErr(w, http.StatusBadRequest, "BadParameter", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}
