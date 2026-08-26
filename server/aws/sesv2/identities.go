package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveIdentities routes /identities and its sub-paths.
func (h *Handler) serveIdentities(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createIdentity(w, r)
		case http.MethodGet:
			h.listIdentities(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		h.serveIdentityByName(w, r, rest[0])
	default:
		h.serveIdentitySub(w, r, rest[0], rest[1:])
	}
}

func (h *Handler) serveIdentityByName(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getIdentity(w, r, name)
	case http.MethodDelete:
		h.deleteIdentity(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveIdentitySub(w http.ResponseWriter, r *http.Request, name string, sub []string) {
	if sub[0] == "policies" {
		h.serveIdentityPolicies(w, r, name, sub[1:])

		return
	}

	if sub[0] == segDkim && len(sub) == twoSegments && sub[1] == "signing" {
		if r.Method != http.MethodPut {
			methodNotAllowed(w)

			return
		}

		h.putIdentityDkimSigning(w, r, name)

		return
	}

	if len(sub) != 1 || r.Method != http.MethodPut {
		notFound(w, r.URL.Path)

		return
	}

	h.putIdentityAttribute(w, r, name, sub[0])
}

func (h *Handler) putIdentityAttribute(w http.ResponseWriter, r *http.Request, name, attr string) {
	switch attr {
	case segDkim:
		h.putIdentityDkim(w, r, name)
	case "mail-from":
		h.putIdentityMailFrom(w, r, name)
	case "configuration-set":
		h.putIdentityConfigSet(w, r, name)
	case "feedback":
		h.putIdentityFeedback(w, r, name)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveIdentityPolicies(w http.ResponseWriter, r *http.Request, name string, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		h.getIdentityPolicies(w, r, name)
	case 1:
		switch r.Method {
		case http.MethodPost:
			h.createIdentityPolicy(w, r, name, rest[0])
		case http.MethodPut:
			h.updateIdentityPolicy(w, r, name, rest[0])
		case http.MethodDelete:
			h.deleteIdentityPolicy(w, r, name, rest[0])
		default:
			methodNotAllowed(w)
		}
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) createIdentityPolicy(w http.ResponseWriter, r *http.Request, name, policyName string) {
	var req identityPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.CreateEmailIdentityPolicy(r.Context(), name, policyName, req.Policy))
}

func (h *Handler) updateIdentityPolicy(w http.ResponseWriter, r *http.Request, name, policyName string) {
	var req identityPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.UpdateEmailIdentityPolicy(r.Context(), name, policyName, req.Policy))
}

func (h *Handler) deleteIdentityPolicy(w http.ResponseWriter, r *http.Request, name, policyName string) {
	writeOK(w, h.ses.DeleteEmailIdentityPolicy(r.Context(), name, policyName))
}

func (h *Handler) getIdentityPolicies(w http.ResponseWriter, r *http.Request, name string) {
	policies, err := h.ses.GetEmailIdentityPolicies(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getIdentityPoliciesResponse{Policies: policies})
}

func (h *Handler) putIdentityDkimSigning(w http.ResponseWriter, r *http.Request, name string) {
	var req putDkimSigningRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	origin := ""
	if req.SigningAttributesOrigin != "" {
		origin = req.SigningAttributesOrigin
	}

	tokens, err := h.ses.PutEmailIdentityDkimSigningAttributes(r.Context(), name, origin)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, putDkimSigningResponse{DkimStatus: "SUCCESS", DkimTokens: tokens})
}

func (h *Handler) putIdentityConfigSet(w http.ResponseWriter, r *http.Request, name string) {
	var req putIdentityConfigSetRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutEmailIdentityConfigurationSetAttributes(r.Context(), name, req.ConfigurationSetName))
}

func (h *Handler) putIdentityFeedback(w http.ResponseWriter, r *http.Request, name string) {
	var req putIdentityFeedbackRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutEmailIdentityFeedbackAttributes(r.Context(), name, req.EmailForwardingEnabled))
}

func (h *Handler) createIdentity(w http.ResponseWriter, r *http.Request) {
	var req createEmailIdentityRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	id, err := h.ses.CreateEmailIdentity(r.Context(), driver.CreateIdentityInput{
		EmailIdentity:        req.EmailIdentity,
		ConfigurationSetName: req.ConfigurationSetName,
		Tags:                 tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createEmailIdentityResponse{
		IdentityType:             id.Type,
		VerifiedForSendingStatus: id.VerifiedForSendingStatus,
		DkimAttributes:           identityToDkimJSON(id),
	})
}

func (h *Handler) getIdentity(w http.ResponseWriter, r *http.Request, name string) {
	id, err := h.ses.GetEmailIdentity(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	resp := getEmailIdentityResponse{
		IdentityType:             id.Type,
		FeedbackForwardingStatus: id.FeedbackForwardingStatus,
		VerifiedForSendingStatus: id.VerifiedForSendingStatus,
		VerificationStatus:       id.VerificationStatus,
		ConfigurationSetName:     id.ConfigurationSetName,
		DkimAttributes:           identityToDkimJSON(id),
		Policies:                 id.Policies,
		Tags:                     mapToTags(id.Tags),
	}

	if id.MailFromDomain != "" {
		resp.MailFromAttributes = &mailFromAttributesJSON{
			MailFromDomain:       id.MailFromDomain,
			MailFromDomainStatus: id.MailFromDomainStatus,
			BehaviorOnMxFailure:  id.MailFromBehaviorOnMxFail,
		}
	}

	writeJSON(w, resp)
}

func (h *Handler) deleteIdentity(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.ses.DeleteEmailIdentity(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listIdentities(w http.ResponseWriter, r *http.Request) {
	ids, err := h.ses.ListEmailIdentities(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	start, end, next := pageWindow(len(ids), r.URL.Query())
	ids = ids[start:end]

	out := make([]identityInfoJSON, 0, len(ids))
	for i := range ids {
		out = append(out, identityInfoJSON{
			IdentityName:       ids[i].Name,
			IdentityType:       ids[i].Type,
			SendingEnabled:     ids[i].VerifiedForSendingStatus,
			VerificationStatus: ids[i].VerificationStatus,
		})
	}

	writeJSON(w, listEmailIdentitiesResponse{EmailIdentities: out, NextToken: next})
}

func (h *Handler) putIdentityDkim(w http.ResponseWriter, r *http.Request, name string) {
	var req putDkimAttributesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.ses.PutEmailIdentityDkimAttributes(r.Context(), name, req.SigningEnabled); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) putIdentityMailFrom(w http.ResponseWriter, r *http.Request, name string) {
	var req putMailFromAttributesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := h.ses.PutEmailIdentityMailFromAttributes(r.Context(), name, req.MailFromDomain, req.BehaviorOnMxFailure)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}
