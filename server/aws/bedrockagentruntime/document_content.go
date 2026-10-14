package bedrockagentruntime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Presigned-URL constants: the token query parameter, and the lifetime the URL
// advertises (it matches the provider's token lifetime).
const (
	tokenParam     = "X-Cloudemu-Content" //nolint:gosec // G101: a query parameter name, not a credential.
	presignExpires = "900"
)

// contentReader serves the content behind a GetDocumentContent token. The AWS
// mock implements it; it is not an API operation.
type contentReader interface {
	ReadDocumentContent(ctx context.Context, token string) (body []byte, mimeType string, err error)
}

// serveDocumentContent handles GetDocumentContent: it returns a presigned URL
// on this server that serves the document's content for 15 minutes.
func serveDocumentContent(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "GetDocumentContent", func(c driver.DocumentReader) {
		var in struct {
			OutputFormat string `json:"outputFormat"`
		}

		if !decodeJSONAllowEmpty(w, r, &in) {
			return
		}

		doc, err := c.GetDocumentContent(r.Context(), segs[1], segs[3], segs[5], in.OutputFormat)
		if err != nil {
			writeErr(w, err)

			return
		}

		// Keep the request's escaping: a document id such as an S3 URI holds
		// slashes that must stay inside one path segment.
		u := url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.EscapedPath()}
		if r.TLS != nil {
			u.Scheme = "https"
		}

		q := url.Values{}
		q.Set(tokenParam, doc.Token)
		q.Set("X-Amz-Expires", presignExpires)
		u.RawQuery = q.Encode()

		writeJSON(w, map[string]any{"mimeType": doc.MimeType, "presignedUrl": u.String(), "documentContentLength": doc.Length})
	})
}

// serveDocumentDownload serves the content behind a presigned URL.
func serveDocumentDownload(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	reader, ok := h.runtime.(contentReader)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "document content is not available")

		return
	}

	body, mime, err := reader.ReadDocumentContent(r.Context(), r.URL.Query().Get(tokenParam))
	if err != nil {
		writeErr(w, err)

		return
	}

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
