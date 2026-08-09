package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveOutbound routes /outbound-emails (SendEmail).
func (h *Handler) serveOutbound(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 {
		notFound(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	h.sendEmail(w, r)
}

func (h *Handler) sendEmail(w http.ResponseWriter, r *http.Request) {
	var req sendEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := driver.SendEmailInput{
		FromAddress:          req.FromEmailAddress,
		ConfigurationSetName: req.ConfigurationSetName,
	}
	if req.Destination != nil {
		in.ToAddresses = req.Destination.ToAddresses
		in.CcAddresses = req.Destination.CcAddresses
		in.BccAddresses = req.Destination.BccAddresses
	}

	applyContent(&in, req.Content)

	msgID, err := h.ses.SendEmail(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, sendEmailResponse{MessageID: msgID})
}

// applyContent copies the simple/templated message content into the driver input.
func applyContent(in *driver.SendEmailInput, c *emailContentJSON) {
	if c == nil {
		return
	}

	applySimple(in, c.Simple)

	if c.Template != nil {
		in.TemplateName = c.Template.TemplateName
		in.TemplateData = c.Template.TemplateData
	}
}

// applySimple copies a simple (subject + body) message into the driver input.
func applySimple(in *driver.SendEmailInput, m *messageJSON) {
	if m == nil {
		return
	}

	if m.Subject != nil {
		in.Subject = m.Subject.Data
	}

	if m.Body == nil {
		return
	}

	if m.Body.HTML != nil {
		in.HTMLBody = m.Body.HTML.Data
	}

	if m.Body.Text != nil {
		in.TextBody = m.Body.Text.Data
	}
}
