package privateca

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

// caTransition is a certificate-authority state-machine verb bound to its driver
// method (:enable, :disable, :undelete).
type caTransition func(ctx context.Context, project, location, caPool, id string) (
	*pcadriver.Resource, *pcadriver.Operation, error)

// activateRequest is the :activate body: the CA-signed certificate a subordinate
// CA is activated with (subordinateConfig is accepted but not modeled).
type activateRequest struct {
	PemCaCertificate string `json:"pemCaCertificate"`
}

// revokeRequest is the :revoke body: the reason a certificate is revoked.
type revokeRequest struct {
	Reason string `json:"reason"`
}

// fetchResponse mirrors FetchCertificateAuthorityCsrResponse.
type fetchResponse struct {
	PemCsr string `json:"pemCsr"`
}

// serveVerb dispatches a resource verb (…/{id}:verb) to the collection it belongs
// to. All verbs are POST except certificateAuthorities:fetch, which the real API
// (and the generated client) issues as a GET.
func (h *Handler) serveVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	want := http.MethodPost
	if rt.verb == verbFetch {
		want = http.MethodGet
	}

	if r.Method != want {
		writeMethodNotAllowed(w)
		return
	}

	switch rt.coll {
	case authoritiesColl:
		h.serveAuthorityVerb(w, r, rt)
	case certificatesColl:
		h.serveCertificateVerb(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb for "+rt.coll)
	}
}

// serveAuthorityVerb drives the certificate-authority state machine
// (:enable/:disable/:undelete/:activate return an LRO; :fetch returns the CSR
// synchronously). An illegal transition surfaces as the real FAILED_PRECONDITION.
func (h *Handler) serveAuthorityVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	m := metas()[authoritiesColl]

	switch rt.verb {
	case "enable":
		h.runTransition(w, r, rt, m, h.db.EnableCertificateAuthority)
	case "disable":
		h.runTransition(w, r, rt, m, h.db.DisableCertificateAuthority)
	case "undelete":
		h.runTransition(w, r, rt, m, h.db.UndeleteCertificateAuthority)
	case "activate":
		h.activateAuthority(w, r, rt, m)
	case verbFetch:
		h.fetchAuthorityCSR(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported certificateAuthorities verb: "+rt.verb)
	}
}

// runTransition invokes a state-machine verb and writes the completed LRO, or the
// canonical error for an illegal transition / missing CA.
func (h *Handler) runTransition(w http.ResponseWriter, r *http.Request, rt *route, m meta, fn caTransition) {
	res, op, err := fn(r.Context(), rt.project, rt.location, rt.pool, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, m, op, res)
}

// activateAuthority reads the signed pemCaCertificate and activates a subordinate
// CA awaiting activation.
func (h *Handler) activateAuthority(w http.ResponseWriter, r *http.Request, rt *route, m meta) {
	var body activateRequest
	if !decodeVerbBody(w, r, &body) {
		return
	}

	res, op, err := h.db.ActivateCertificateAuthority(
		r.Context(), rt.project, rt.location, rt.pool, rt.name, body.PemCaCertificate)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, m, op, res)
}

// fetchAuthorityCSR returns the deterministic PEM CSR of a subordinate CA awaiting
// activation.
func (h *Handler) fetchAuthorityCSR(w http.ResponseWriter, r *http.Request, rt *route) {
	csr, err := h.db.FetchCertificateAuthorityCSR(r.Context(), rt.project, rt.location, rt.pool, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, fetchResponse{PemCsr: csr})
}

// serveCertificateVerb handles a certificate verb (:revoke), which completes
// synchronously and returns the updated certificate.
func (h *Handler) serveCertificateVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.verb != "revoke" {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported certificates verb: "+rt.verb)
		return
	}

	var body revokeRequest
	if !decodeVerbBody(w, r, &body) {
		return
	}

	res, err := h.db.RevokeCertificate(r.Context(), rt.project, rt.location, rt.pool, rt.name, body.Reason)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeResource(w, metas()[certificatesColl], res)
}

// decodeVerbBody reads and JSON-decodes a verb request body into dst. An empty
// body is accepted (dst keeps its zero value).
func decodeVerbBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return false
	}

	if len(raw) == 0 {
		return true
	}

	if err := json.Unmarshal(normalizeEnumNumbers(raw), dst); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
		return false
	}

	return true
}
