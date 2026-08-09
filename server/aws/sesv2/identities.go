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
		h.serveIdentitySub(w, r, rest[0], rest[1])
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

func (h *Handler) serveIdentitySub(w http.ResponseWriter, r *http.Request, name, sub string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	switch sub {
	case "dkim":
		h.putIdentityDkim(w, r, name)
	case "mail-from":
		h.putIdentityMailFrom(w, r, name)
	default:
		notFound(w, r.URL.Path)
	}
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

	out := make([]identityInfoJSON, 0, len(ids))
	for i := range ids {
		out = append(out, identityInfoJSON{
			IdentityName:       ids[i].Name,
			IdentityType:       ids[i].Type,
			SendingEnabled:     ids[i].VerifiedForSendingStatus,
			VerificationStatus: ids[i].VerificationStatus,
		})
	}

	writeJSON(w, listEmailIdentitiesResponse{EmailIdentities: out})
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
