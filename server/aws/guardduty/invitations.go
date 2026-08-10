package guardduty

import "net/http"

// serveAdministrator routes /detector/{id}/administrator: POST=AcceptAdministrator
// Invitation, GET=GetAdministratorAccount, /disassociate=Disassociate.
func (h *Handler) serveAdministrator(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	ctx := r.Context()

	if len(rest) == 1 && rest[0] == "disassociate" {
		body, err := h.gd.DisassociateFromAdministratorAccount(ctx, id)
		h.writeResult(w, body, err)

		return
	}

	switch r.Method {
	case http.MethodPost:
		body, err := h.gd.AcceptAdministratorInvitation(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case http.MethodGet:
		body, err := h.gd.GetAdministratorAccount(ctx, id)
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}

// serveMaster routes /detector/{id}/master: the legacy master-naming twin of
// serveAdministrator, reading and writing the same administrator link.
func (h *Handler) serveMaster(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	ctx := r.Context()

	if len(rest) == 1 && rest[0] == "disassociate" {
		body, err := h.gd.DisassociateFromMasterAccount(ctx, id)
		h.writeResult(w, body, err)

		return
	}

	switch r.Method {
	case http.MethodPost:
		body, err := h.gd.AcceptInvitation(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case http.MethodGet:
		body, err := h.gd.GetMasterAccount(ctx, id)
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}
