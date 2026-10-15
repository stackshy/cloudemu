package vpclattice

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

const (
	defaultMaxResults = 100
	maxMaxResults     = 100
)

// pageParams reads maxResults (1-100, default 100) and nextToken from the query,
// writing a ValidationException and returning false when either is invalid.
func pageParams(w http.ResponseWriter, r *http.Request) (maxResults int, token string, ok bool) {
	maxResults = defaultMaxResults

	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxMaxResults {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"maxResults must be an integer between 1 and "+strconv.Itoa(maxMaxResults))

			return 0, "", false
		}

		maxResults = n
	}

	return maxResults, r.URL.Query().Get("nextToken"), true
}

// writeItems paginates an already deterministically ordered driver slice and
// writes the restJson1 list body {"items": [...], "nextToken": "..."}.
func writeItems[D, W any](w http.ResponseWriter, r *http.Request, all []D, toWire func(*D) W) {
	maxResults, token, ok := pageParams(w, r)
	if !ok {
		return
	}

	page, err := pagination.Paginate(all, token, maxResults)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid nextToken")

		return
	}

	items := make([]W, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, toWire(&page.Items[i]))
	}

	body := map[string]any{"items": items}
	if page.HasMore {
		body["nextToken"] = page.NextPageToken
	}

	writeJSON(w, body)
}
