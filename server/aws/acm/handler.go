// Package acm implements the AWS Certificate Manager JSON 1.1 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/acm client (or the
// `aws acm` CLI) at a Server registered with this handler and certificate
// operations run against an in-memory ACM driver.
//
// ACM uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "CertificateManager.").
package acm

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	acmdriver "github.com/stackshy/cloudemu/v2/services/acm/driver"
)

const targetPrefix = "CertificateManager."

// Handler serves ACM JSON-RPC requests against an ACM driver.
type Handler struct {
	acm    acmdriver.ACM
	routes map[string]http.HandlerFunc
}

// New returns an ACM handler backed by d.
func New(d acmdriver.ACM) *Handler {
	h := &Handler{acm: d}
	h.routes = map[string]http.HandlerFunc{
		"RequestCertificate":        h.requestCertificate,
		"ImportCertificate":         h.importCertificate,
		"DescribeCertificate":       h.describeCertificate,
		"ListCertificates":          h.listCertificates,
		"DeleteCertificate":         h.deleteCertificate,
		"GetCertificate":            h.getCertificate,
		"ExportCertificate":         h.exportCertificate,
		"RenewCertificate":          h.renewCertificate,
		"ResendValidationEmail":     h.resendValidationEmail,
		"UpdateCertificateOptions":  h.updateCertificateOptions,
		"RevokeCertificate":         h.revokeCertificate,
		"SearchCertificates":        h.searchCertificates,
		"AddTagsToCertificate":      h.addTags,
		"RemoveTagsFromCertificate": h.removeTags,
		"ListTagsForCertificate":    h.listTags,
		"GetAccountConfiguration":   h.getAccountConfiguration,
		"PutAccountConfiguration":   h.putAccountConfiguration,
	}

	return h
}

// Matches returns true for ACM-shaped requests (X-Amz-Target of
// "CertificateManager.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches ACM operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported ACM operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error).
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, out)
}

// writeErr maps a driver error to the closest ACM JSON error type.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceInUseException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalException", err.Error())
	}
}
