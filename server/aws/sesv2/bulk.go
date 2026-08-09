package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveBulkOutbound routes /outbound-bulk-emails (SendBulkEmail).
func (h *Handler) serveBulkOutbound(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req sendBulkEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := driver.SendBulkEmailInput{
		FromAddress:          req.FromEmailAddress,
		ConfigurationSetName: req.ConfigurationSetName,
	}

	if req.DefaultContent != nil && req.DefaultContent.Template != nil {
		in.TemplateName = req.DefaultContent.Template.TemplateName
		in.DefaultTemplateData = req.DefaultContent.Template.TemplateData
	}

	for _, e := range req.BulkEmailEntries {
		entry := driver.BulkEmailEntry{}
		if e.Destination != nil {
			entry.ToAddresses = e.Destination.ToAddresses
			entry.CcAddresses = e.Destination.CcAddresses
			entry.BccAddresses = e.Destination.BccAddresses
		}

		in.Entries = append(in.Entries, entry)
	}

	results, err := h.ses.SendBulkEmail(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]bulkEmailResultJSON, 0, len(results))
	for i := range results {
		out = append(out, bulkEmailResultJSON{Status: results[i].Status, MessageID: results[i].MessageID})
	}

	writeJSON(w, sendBulkEmailResponse{BulkEmailEntryResults: out})
}
