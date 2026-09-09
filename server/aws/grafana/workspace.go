package grafana

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateWorkspaceInput
	if !decodeBody(w, r, &in) {
		return
	}

	out, err := h.g.CreateWorkspace(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"workspace": workspaceToWire(out)})
}

func (h *Handler) describeWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.g.DescribeWorkspace(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspaceToWire(out)})
}

func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	var in driver.UpdateWorkspaceInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.ID = id

	out, err := h.g.UpdateWorkspace(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"workspace": workspaceToWire(out)})
}

func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.g.DeleteWorkspace(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"workspace": workspaceToWire(out)})
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	items, next, err := h.g.ListWorkspaces(r.Context(), driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(items))
	for i := range items {
		summaries = append(summaries, workspaceSummaryToWire(&items[i]))
	}

	body := map[string]any{"workspaces": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, http.StatusOK, body)
}

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow; Grafana's maxResults tops out well below this.
const maxQueryInt = 1 << 20

// atoiDefault parses a non-negative query int32, returning 0 when empty or
// invalid. It is bounded by maxQueryInt so the conversion cannot overflow.
func atoiDefault(s string) int32 {
	n := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}

		n = n*10 + int(c-'0')
		if n > maxQueryInt {
			return maxQueryInt
		}
	}

	return int32(n)
}
