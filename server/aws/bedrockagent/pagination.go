package bedrockagent

import (
	"net/http"
	"strconv"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// listRequest is the maxResults/nextToken body the POST List ops send.
type listRequest struct {
	MaxResults *int32 `json:"maxResults"`
	NextToken  string `json:"nextToken"`
}

func (l listRequest) page() badriver.Page {
	return badriver.Page{MaxResults: l.MaxResults, NextToken: l.NextToken}
}

// decodeListBody reads a POST List op's paging body (which may be empty).
func decodeListBody(w http.ResponseWriter, r *http.Request) (badriver.Page, bool) {
	var in listRequest
	if !decodeBody(w, r, &in) {
		return badriver.Page{}, false
	}

	return in.page(), true
}

// queryPage reads the maxResults/nextToken query parameters the GET List ops
// send.
func queryPage(w http.ResponseWriter, r *http.Request) (badriver.Page, bool) {
	q := r.URL.Query()
	page := badriver.Page{NextToken: q.Get("nextToken")}

	if raw := q.Get("maxResults"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"1 validation error detected: Value '"+raw+"' at 'maxResults' failed to satisfy constraint: "+
					"Member must be an integer")

			return badriver.Page{}, false
		}

		size := int32(n)
		page.MaxResults = &size
	}

	return page, true
}
