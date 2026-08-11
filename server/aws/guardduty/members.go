package guardduty

import "net/http"

// serveMember routes /detector/{id}/member and its action sub-paths.
//
//nolint:gocyclo // one arm per member sub-path; large by API design.
func (h *Handler) serveMember(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			body, err := h.gd.CreateMembers(ctx, id, rawBody(r))
			h.writeResult(w, body, err)
		case http.MethodGet:
			body, err := h.gd.ListMembers(ctx, id, pageFromQuery(r))
			h.writeResult(w, body, err)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch rest[0] {
	case segDelete:
		body, err := h.gd.DeleteMembers(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case segGet:
		body, err := h.gd.GetMembers(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "invite":
		body, err := h.gd.InviteMembers(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case segDisassociate:
		body, err := h.gd.DisassociateMembers(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case segStart:
		body, err := h.gd.StartMonitoringMembers(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "stop":
		body, err := h.gd.StopMonitoringMembers(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "detector":
		h.serveMemberDetector(w, r, id, rest[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveMemberDetector routes /detector/{id}/member/detector/{get|update}.
func (h *Handler) serveMemberDetector(w http.ResponseWriter, r *http.Request, id string, rest []string) {
	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	ctx := r.Context()

	switch rest[0] {
	case segGet:
		body, err := h.gd.GetMemberDetectors(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	case "update":
		body, err := h.gd.UpdateMemberDetectors(ctx, id, rawBody(r))
		h.writeResult(w, body, err)
	default:
		notFoundPath(w, r.URL.Path)
	}
}
