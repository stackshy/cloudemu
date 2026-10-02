package backup

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// serveTags handles the shared /tags/{resourceArn} operations: POST
// (TagResource) and GET (ListTags). The ARN is the whole path below /tags/.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	arn := strings.Join(rest, "/")
	if arn == "" {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r, arn)
	case http.MethodGet:
		h.listTags(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

// serveUntag handles POST /untag/{resourceArn} (UntagResource).
func (h *Handler) serveUntag(w http.ResponseWriter, r *http.Request, rest []string) {
	arn := strings.Join(rest, "/")
	if arn == "" {
		notFoundPath(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var req struct {
		TagKeyList []string `json:"TagKeyList"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.backup.UntagResource(r.Context(), arn, req.TagKeyList); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		Tags map[string]string `json:"Tags"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.backup.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeEmpty(w)
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, arn string) {
	page, ok := strictPageFromQuery(w, r)
	if !ok {
		return
	}

	tags, next, err := h.backup.ListTags(r.Context(), arn, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{}
	if len(tags) > 0 {
		body["Tags"] = tags
	}

	putString(body, "NextToken", next)

	writeJSON(w, body)
}

// strictPageFromQuery reads maxResults/nextToken like pageFromQuery, but a
// maxResults that is present and not a positive integer is rejected with
// InvalidParameterValueException instead of falling back to the default. The
// provider enforces the upper bound.
func strictPageFromQuery(w http.ResponseWriter, r *http.Request) (driver.Page, bool) {
	q := r.URL.Query()
	page := driver.Page{NextToken: q.Get("nextToken")}

	raw, present := q["maxResults"]
	if !present {
		return page, true
	}

	n, err := strconv.ParseInt(raw[0], 10, 32)
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, driver.ExInvalidParameter, "maxResults must be a positive integer")

		return driver.Page{}, false
	}

	page.MaxResults = int32(n)

	return page, true
}
