package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveTemplates routes /templates and its sub-paths.
func (h *Handler) serveTemplates(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createTemplate(w, r)
		case http.MethodGet:
			h.listTemplates(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		h.serveTemplateByName(w, r, rest[0])
	case twoSegments:
		if rest[1] == "render" && r.Method == http.MethodPost {
			h.testRenderTemplate(w, r, rest[0])

			return
		}

		notFound(w, r.URL.Path)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveTemplateByName(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getTemplate(w, r, name)
	case http.MethodPut:
		h.updateTemplate(w, r, name)
	case http.MethodDelete:
		h.deleteTemplate(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) {
	var req createEmailTemplateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := h.ses.CreateEmailTemplate(r.Context(), driver.TemplateInput{
		Name:    req.TemplateName,
		Content: contentToDriver(req.TemplateContent),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request, name string) {
	tpl, err := h.ses.GetEmailTemplate(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getEmailTemplateResponse{
		TemplateName:    tpl.Name,
		TemplateContent: contentToWire(tpl.Content),
	})
}

func (h *Handler) updateTemplate(w http.ResponseWriter, r *http.Request, name string) {
	var req updateEmailTemplateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := h.ses.UpdateEmailTemplate(r.Context(), driver.TemplateInput{
		Name:    name,
		Content: contentToDriver(req.TemplateContent),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) deleteTemplate(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.ses.DeleteEmailTemplate(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) {
	tpls, err := h.ses.ListEmailTemplates(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]templateMetadataJSON, 0, len(tpls))
	for i := range tpls {
		out = append(out, templateMetadataJSON{
			TemplateName: tpls[i].Name,
			CreatedTime:  epochSeconds(tpls[i].CreatedAt),
		})
	}

	writeJSON(w, listEmailTemplatesResponse{TemplatesMetadata: out})
}

func (h *Handler) testRenderTemplate(w http.ResponseWriter, r *http.Request, name string) {
	var req testRenderRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rendered, err := h.ses.TestRenderEmailTemplate(r.Context(), name, req.TemplateData)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, testRenderResponse{RenderedTemplate: rendered})
}
