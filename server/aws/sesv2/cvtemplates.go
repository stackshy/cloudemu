package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveCVTemplates routes /custom-verification-email-templates and sub-paths.
func (h *Handler) serveCVTemplates(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createCVTemplate(w, r)
		case http.MethodGet:
			h.listCVTemplates(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		switch r.Method {
		case http.MethodGet:
			h.getCVTemplate(w, r, rest[0])
		case http.MethodPut:
			h.updateCVTemplate(w, r, rest[0])
		case http.MethodDelete:
			h.deleteCVTemplate(w, r, rest[0])
		default:
			methodNotAllowed(w)
		}
	default:
		notFound(w, r.URL.Path)
	}
}

func cvInput(name string, req *cvTemplateRequest) driver.CustomVerificationEmailTemplateInput {
	return driver.CustomVerificationEmailTemplateInput{
		TemplateName:       name,
		FromEmailAddress:   req.FromEmailAddress,
		TemplateSubject:    req.TemplateSubject,
		TemplateContent:    req.TemplateContent,
		SuccessRedirectURL: req.SuccessRedirectionURL,
		FailureRedirectURL: req.FailureRedirectionURL,
	}
}

func (h *Handler) createCVTemplate(w http.ResponseWriter, r *http.Request) {
	var req cvTemplateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.CreateCustomVerificationEmailTemplate(r.Context(), cvInput(req.TemplateName, &req)))
}

func (h *Handler) updateCVTemplate(w http.ResponseWriter, r *http.Request, name string) {
	var req cvTemplateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.UpdateCustomVerificationEmailTemplate(r.Context(), cvInput(name, &req)))
}

func (h *Handler) getCVTemplate(w http.ResponseWriter, r *http.Request, name string) {
	t, err := h.ses.GetCustomVerificationEmailTemplate(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, cvTemplateResponse{
		TemplateName:          t.TemplateName,
		FromEmailAddress:      t.FromEmailAddress,
		TemplateSubject:       t.TemplateSubject,
		TemplateContent:       t.TemplateContent,
		SuccessRedirectionURL: t.SuccessRedirectURL,
		FailureRedirectionURL: t.FailureRedirectURL,
	})
}

func (h *Handler) deleteCVTemplate(w http.ResponseWriter, r *http.Request, name string) {
	writeOK(w, h.ses.DeleteCustomVerificationEmailTemplate(r.Context(), name))
}

func (h *Handler) listCVTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.ses.ListCustomVerificationEmailTemplates(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]cvTemplateMetadataJSON, 0, len(templates))
	for i := range templates {
		out = append(out, cvTemplateMetadataJSON{
			TemplateName:          templates[i].TemplateName,
			FromEmailAddress:      templates[i].FromEmailAddress,
			TemplateSubject:       templates[i].TemplateSubject,
			SuccessRedirectionURL: templates[i].SuccessRedirectURL,
			FailureRedirectionURL: templates[i].FailureRedirectURL,
		})
	}

	writeJSON(w, listCVTemplatesResponse{CustomVerificationEmailTemplates: out})
}

// serveCVOutbound routes /outbound-custom-verification-emails (POST).
func (h *Handler) serveCVOutbound(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req sendCVEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	msgID, err := h.ses.SendCustomVerificationEmail(r.Context(), req.TemplateName, req.EmailAddress, req.ConfigurationSetName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, sendEmailResponse{MessageID: msgID})
}
