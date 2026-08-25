package keyvault

import (
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// defaultIssuer is echoed on a create operation whose policy names no issuer.
//
//nolint:gochecknoglobals // read-only default response fragment, not mutable state
var defaultIssuer = json.RawMessage(`{"name":"Self"}`)

// writeJSONStatus writes v as an application/json response with an explicit
// status (create returns 202 Accepted rather than the 200 writeJSON emits).
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

// writeCertErr maps a canonical cloudemu error to a Key Vault certificate error
// response.
//
//nolint:dupl // parallel certificate/secret error mapper; the shared shape is intentional
func writeCertErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "CertificateNotFound", err.Error())
	case cerrors.IsAlreadyExists(err) && strings.Contains(err.Error(), "deleted but recoverable"):
		writeErrInner(w, http.StatusConflict, "Conflict", err.Error(), "ObjectIsDeletedButRecoverable")
	case cerrors.IsAlreadyExists(err):
		writeErr(w, http.StatusConflict, "Conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeErr(w, http.StatusBadRequest, "BadParameter", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}

func (h *CertsHandler) createCertificate(w http.ResponseWriter, r *http.Request, name string) {
	var req createCertRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	params := secretsdriver.KVCreateCertificateParams{
		Tags:       req.Tags,
		Attributes: attrsFromRequest(req.Attributes),
		PolicyRaw:  req.Policy,
	}

	issuer := defaultIssuer

	if len(req.Policy) > 0 {
		var policy certPolicyParse
		if err := json.Unmarshal(req.Policy, &policy); err != nil {
			writeErr(w, http.StatusBadRequest, "BadParameter", "invalid certificate policy")
			return
		}

		applyPolicy(&params, &policy)

		if len(policy.Issuer) > 0 {
			issuer = policy.Issuer
		}
	}

	cert, err := h.kv.CreateCertificate(r.Context(), vaultFromRequest(r), name, &params)
	if err != nil {
		writeCertErr(w, err)
		return
	}

	// The self-signed certificate is generated synchronously, so the operation
	// is already completed and its target points at the finished certificate.
	writeJSONStatus(w, http.StatusAccepted, certOperationJSON{
		ID:            certID(r, name, pendingSeg),
		Issuer:        issuer,
		Status:        "completed",
		StatusDetails: "self-signed certificate issued",
		Target:        certID(r, cert.Name, cert.Version),
	})
}

// applyPolicy copies the self-signed generation inputs out of a parsed policy.
func applyPolicy(params *secretsdriver.KVCreateCertificateParams, policy *certPolicyParse) {
	if sp := policy.SecretProps; sp != nil {
		params.ContentType = sp.ContentType
	}

	if xp := policy.X509Props; xp != nil {
		params.Subject = xp.Subject
		params.ValidityMonths = xp.ValidityMonths

		if xp.Sans != nil {
			params.DNSNames = xp.Sans.DNSNames
		}
	}
}

func (h *CertsHandler) getCertificate(w http.ResponseWriter, r *http.Request, name, version string) {
	cert, err := h.kv.GetCertificate(r.Context(), vaultFromRequest(r), name, version)
	if err != nil {
		writeCertErr(w, err)
		return
	}

	writeJSON(w, toCertBundle(r, cert))
}

// getCertificatePolicy returns the current certificate version's stored policy
// verbatim. Policy update, issuers and contacts are not yet implemented.
func (h *CertsHandler) getCertificatePolicy(w http.ResponseWriter, r *http.Request, name string) {
	cert, err := h.kv.GetCertificate(r.Context(), vaultFromRequest(r), name, "")
	if err != nil {
		writeCertErr(w, err)
		return
	}

	if len(cert.PolicyRaw) == 0 {
		writeJSON(w, json.RawMessage(`{}`))
		return
	}

	writeJSON(w, json.RawMessage(cert.PolicyRaw))
}

// getCertificateOperation reports the create operation for name as completed,
// since self-signed issuance is synchronous.
func (h *CertsHandler) getCertificateOperation(w http.ResponseWriter, r *http.Request, name string) {
	cert, err := h.kv.GetCertificate(r.Context(), vaultFromRequest(r), name, "")
	if err != nil {
		writeCertErr(w, err)
		return
	}

	writeJSON(w, certOperationJSON{
		ID:            certID(r, name, pendingSeg),
		Issuer:        defaultIssuer,
		Status:        "completed",
		StatusDetails: "self-signed certificate issued",
		Target:        certID(r, cert.Name, cert.Version),
	})
}

func (h *CertsHandler) listCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := h.kv.ListCertificates(r.Context(), vaultFromRequest(r))
	if err != nil {
		writeCertErr(w, err)
		return
	}

	items := make([]certItemJSON, 0, len(certs))
	for i := range certs {
		items = append(items, toCertItem(r, &certs[i]))
	}

	writeJSON(w, certListResponseJSON{Value: items})
}

func (h *CertsHandler) listCertificateVersions(w http.ResponseWriter, r *http.Request, name string) {
	versions, err := h.kv.ListCertificateVersions(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCertErr(w, err)
		return
	}

	items := make([]certItemJSON, 0, len(versions))
	for i := range versions {
		items = append(items, toCertItem(r, &versions[i]))
	}

	writeJSON(w, certListResponseJSON{Value: items})
}

func (h *CertsHandler) deleteCertificate(w http.ResponseWriter, r *http.Request, name string) {
	deleted, err := h.kv.DeleteCertificate(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCertErr(w, err)
		return
	}

	writeJSON(w, toDeletedCertBundle(r, deleted))
}

func (h *CertsHandler) listDeletedCertificates(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.kv.ListDeletedCertificates(r.Context(), vaultFromRequest(r))
	if err != nil {
		writeCertErr(w, err)
		return
	}

	items := make([]deletedCertItemJSON, 0, len(deleted))
	for i := range deleted {
		items = append(items, toDeletedCertItem(r, &deleted[i]))
	}

	writeJSON(w, deletedCertListResponseJSON{Value: items})
}

func (h *CertsHandler) getDeletedCertificate(w http.ResponseWriter, r *http.Request, name string) {
	deleted, err := h.kv.GetDeletedCertificate(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCertErr(w, err)
		return
	}

	writeJSON(w, toDeletedCertBundle(r, deleted))
}

func (h *CertsHandler) recoverDeletedCertificate(w http.ResponseWriter, r *http.Request, name string) {
	cert, err := h.kv.RecoverDeletedCertificate(r.Context(), vaultFromRequest(r), name)
	if err != nil {
		writeCertErr(w, err)
		return
	}

	writeJSON(w, toCertBundle(r, cert))
}

func (h *CertsHandler) purgeDeletedCertificate(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.kv.PurgeDeletedCertificate(r.Context(), vaultFromRequest(r), name); err != nil {
		writeCertErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
