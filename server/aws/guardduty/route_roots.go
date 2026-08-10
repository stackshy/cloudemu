package guardduty

import "net/http"

// This file routes the top-level (non-detector) GuardDuty roots: tags, admin
// (org admin accounts), invitation, organization, malware-scan, malware-scans,
// malware-protection-plan, and object-malware-scan.

// serveTags routes /tags/{ResourceArn}. The ARN is a single (percent-decoded)
// path segment. GET=ListTagsForResource, POST=TagResource, DELETE=UntagResource.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	arn := rest[0]
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		body, err := h.gd.ListTagsForResource(ctx, arn)
		h.writeResult(w, body, err)
	case http.MethodPost:
		body, err := h.gd.TagResource(ctx, arn, rawBody(r))
		h.writeResult(w, body, err)
	case http.MethodDelete:
		body, err := h.gd.UntagResource(ctx, arn, r.URL.Query()["tagKeys"])
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}

// serveAdmin routes /admin: GET=ListOrganizationAdminAccounts,
// POST /admin/enable=EnableOrganizationAdminAccount,
// POST /admin/disable=DisableOrganizationAdminAccount.
func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			body, err := h.gd.ListOrganizationAdminAccounts(ctx, pageFromQuery(r))
			h.writeResult(w, body, err)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) == 1 && r.Method == http.MethodPost {
		switch rest[0] {
		case "enable":
			body, err := h.gd.EnableOrganizationAdminAccount(ctx, rawBody(r))
			h.writeResult(w, body, err)

			return
		case "disable":
			body, err := h.gd.DisableOrganizationAdminAccount(ctx, rawBody(r))
			h.writeResult(w, body, err)

			return
		}
	}

	notFoundPath(w, r.URL.Path)
}

// serveInvitation routes /invitation: GET=ListInvitations,
// GET /invitation/count=GetInvitationsCount,
// POST /invitation/decline=DeclineInvitations,
// POST /invitation/delete=DeleteInvitations.
func (h *Handler) serveInvitation(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			body, err := h.gd.ListInvitations(ctx, pageFromQuery(r))
			h.writeResult(w, body, err)

			return
		}

		methodNotAllowed(w)

		return
	}

	switch {
	case rest[0] == "count" && r.Method == http.MethodGet:
		body, err := h.gd.GetInvitationsCount(ctx)
		h.writeResult(w, body, err)
	case rest[0] == "decline" && r.Method == http.MethodPost:
		body, err := h.gd.DeclineInvitations(ctx, rawBody(r))
		h.writeResult(w, body, err)
	case rest[0] == "delete" && r.Method == http.MethodPost:
		body, err := h.gd.DeleteInvitations(ctx, rawBody(r))
		h.writeResult(w, body, err)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveOrganization routes /organization/statistics=GetOrganizationStatistics.
func (h *Handler) serveOrganization(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 1 && rest[0] == "statistics" && r.Method == http.MethodGet {
		body, err := h.gd.GetOrganizationStatistics(r.Context())
		h.writeResult(w, body, err)

		return
	}

	notFoundPath(w, r.URL.Path)
}

// serveMalwareScan routes /malware-scan: POST /malware-scan/start=StartMalwareScan,
// GET /malware-scan/{ScanId}=GetMalwareScan.
func (h *Handler) serveMalwareScan(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			body, err := h.gd.ListMalwareScans(ctx, rawBody(r))
			h.writeResult(w, body, err)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) == 1 && rest[0] == "start" && r.Method == http.MethodPost {
		body, err := h.gd.StartMalwareScan(ctx, rawBody(r))
		h.writeResult(w, body, err)

		return
	}

	if len(rest) == 1 && r.Method == http.MethodGet {
		body, err := h.gd.GetMalwareScan(ctx, rest[0])
		h.writeResult(w, body, err)

		return
	}

	notFoundPath(w, r.URL.Path)
}

// serveMalwareScans routes POST /malware-scans (ListMalwareScans, filter body).
func (h *Handler) serveMalwareScans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	body, err := h.gd.ListMalwareScans(r.Context(), rawBody(r))
	h.writeResult(w, body, err)
}

// serveObjectMalwareScan routes POST /object-malware-scan/send=SendObjectMalwareScan.
func (h *Handler) serveObjectMalwareScan(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 1 && rest[0] == "send" && r.Method == http.MethodPost {
		body, err := h.gd.SendObjectMalwareScan(r.Context(), rawBody(r))
		h.writeResult(w, body, err)

		return
	}

	notFoundPath(w, r.URL.Path)
}

// serveMalwareProtectionPlan routes /malware-protection-plan:
// POST=CreateMalwareProtectionPlan, GET=ListMalwareProtectionPlans,
// GET/PATCH/DELETE /malware-protection-plan/{id}.
func (h *Handler) serveMalwareProtectionPlan(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := r.Context()

	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			body, err := h.gd.CreateMalwareProtectionPlan(ctx, rawBody(r))
			h.writeResult(w, body, err)
		case http.MethodGet:
			body, err := h.gd.ListMalwareProtectionPlans(ctx, pageFromQuery(r))
			h.writeResult(w, body, err)
		default:
			methodNotAllowed(w)
		}

		return
	}

	planID := rest[0]

	switch r.Method {
	case http.MethodGet:
		body, err := h.gd.GetMalwareProtectionPlan(ctx, planID)
		h.writeResult(w, body, err)
	case http.MethodPatch:
		body, err := h.gd.UpdateMalwareProtectionPlan(ctx, planID, rawBody(r))
		h.writeResult(w, body, err)
	case http.MethodDelete:
		body, err := h.gd.DeleteMalwareProtectionPlan(ctx, planID)
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}
