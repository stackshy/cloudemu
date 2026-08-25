// This file implements the Azure Key Vault certificates data-plane API
// (/certificates/…, /deletedcertificates/…) as a server.Handler. It claims the
// certificate routes on the vault host so certificate requests reach Key Vault
// instead of falling through to the permissive Table/Blob storage fallbacks
// (which mis-parse them into an odata "PartitionKey and RowKey are required"
// error). Real azcertificates clients — and raw REST callers — create
// self-signed certificates and get, list, delete and recover them against the
// KeyVaultCertificates surface of the secrets driver.
//
// Coverage (Key Vault 7.x REST shapes):
//
//	POST   /certificates/{name}/create           — create (self-signed) certificate
//	GET    /certificates/{name}[/{version}]       — get current or specific version
//	GET    /certificates/{name}/versions          — list versions
//	GET    /certificates/{name}/policy            — get certificate policy
//	GET    /certificates/{name}/pending           — get (completed) create operation
//	GET    /certificates                          — list certificates
//	DELETE /certificates/{name}                   — soft-delete certificate
//	GET    /deletedcertificates                   — list deleted certificates
//	GET    /deletedcertificates/{name}            — get deleted certificate
//	POST   /deletedcertificates/{name}/recover    — recover deleted certificate
//	DELETE /deletedcertificates/{name}            — purge deleted certificate
package keyvault

import (
	"net/http"
	"strings"

	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	certsPrefix        = "/certificates"
	deletedCertsPrefix = "/deletedcertificates"
	pendingSeg         = "pending"
	policySeg          = "policy"
)

// CertsHandler serves the Key Vault certificates data-plane API against a
// backend that implements the Azure-specific KeyVaultCertificates surface.
type CertsHandler struct {
	kv secretsdriver.KeyVaultCertificates
}

// NewCerts returns a certificates handler backed by s. s must implement the
// Azure-specific KeyVaultCertificates surface (the Azure provider mock does); a
// backend that does not is served 500 on every certificates data-plane call.
func NewCerts(s secretsdriver.Secrets) *CertsHandler {
	kv, _ := s.(secretsdriver.KeyVaultCertificates)

	return &CertsHandler{kv: kv}
}

// Matches claims /certificates and /deletedcertificates data-plane requests.
// This is the routing fix: without it these paths fall through to the
// permissive storage fallbacks. Disjoint from ARM (/subscriptions/…) and from
// the secrets (/secrets) and keys (/keys) surfaces.
func (*CertsHandler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return p == certsPrefix || strings.HasPrefix(p, certsPrefix+"/") ||
		p == deletedCertsPrefix || strings.HasPrefix(p, deletedCertsPrefix+"/")
}

// ServeHTTP answers the bearer challenge for unauthenticated requests, then
// routes on the path and method.
func (h *CertsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveDataPlane(w, r, h.kv == nil, "Key Vault certificates backend unavailable", dataPlaneRoutes{
		deletedPrefix: deletedCertsPrefix,
		mainPrefix:    certsPrefix,
		routeDeleted:  func(tail string) { h.routeDeleted(w, r, tail) },
		routeMain:     func(tail string) { h.routeCerts(w, r, tail) },
	})
}

func (h *CertsHandler) routeCerts(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			h.listCertificates(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")

		return
	}

	name, sub, hasSub := strings.Cut(tail, "/")
	if !hasSub {
		h.routeBareCert(w, r, name)
		return
	}

	h.routeNamedCert(w, r, name, sub)
}

// routeBareCert dispatches /certificates/{name} requests by method.
func (h *CertsHandler) routeBareCert(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getCertificate(w, r, name, "")
	case http.MethodDelete:
		h.deleteCertificate(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeNamedCert dispatches /certificates/{name}/{sub} requests. sub is a
// reserved segment (create/versions/policy/pending) or a certificate version.
func (h *CertsHandler) routeNamedCert(w http.ResponseWriter, r *http.Request, name, sub string) {
	if h.routeReservedCertSub(w, r, name, sub) {
		return
	}

	switch {
	case strings.Contains(sub, "/"):
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	case r.Method == http.MethodGet:
		h.getCertificate(w, r, name, sub)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}

// routeReservedCertSub handles the reserved sub-segments of
// /certificates/{name} (create/versions/policy/pending), returning true when it
// served the request.
func (h *CertsHandler) routeReservedCertSub(w http.ResponseWriter, r *http.Request, name, sub string) bool {
	switch {
	case sub == createSeg && r.Method == http.MethodPost:
		h.createCertificate(w, r, name)
	case sub == versionsSeg && r.Method == http.MethodGet:
		h.listCertificateVersions(w, r, name)
	case sub == policySeg && r.Method == http.MethodGet:
		h.getCertificatePolicy(w, r, name)
	case sub == pendingSeg && r.Method == http.MethodGet:
		h.getCertificateOperation(w, r, name)
	default:
		return false
	}

	return true
}

// routeDeleted dispatches /deletedcertificates[...] requests. It mirrors the
// secrets and keys soft-delete routers (get/purge/recover).
//
//nolint:dupl // parallel soft-delete router for certificates vs secrets/keys; the shared shape is intentional
func (h *CertsHandler) routeDeleted(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			h.listDeletedCertificates(w, r)
			return
		}

		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")

		return
	}

	name, sub, hasSub := strings.Cut(tail, "/")

	switch {
	case !hasSub && r.Method == http.MethodGet:
		h.getDeletedCertificate(w, r, name)
	case !hasSub && r.Method == http.MethodDelete:
		h.purgeDeletedCertificate(w, r, name)
	case sub == recoverSeg && r.Method == http.MethodPost:
		h.recoverDeletedCertificate(w, r, name)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BadRequest", "unsupported Key Vault operation")
	}
}
