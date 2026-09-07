package aps

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// maxQueryInt bounds a parsed query integer so the result fits an int32 without
// overflow; APS MaxResults tops out well below this.
const maxQueryInt = 1 << 20

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	ws, err := h.aps.CreateWorkspace(r.Context(), &driver.CreateWorkspaceInput{
		Alias:     stringField(raw, "alias"),
		KmsKeyArn: stringField(raw, "kmsKeyArn"),
		Tags:      tagsFromBody(raw),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"arn":         ws.Arn,
		"workspaceId": ws.WorkspaceID,
		"status":      statusBlock(ws.Status),
	}
	if ws.KmsKeyArn != "" {
		body["kmsKeyArn"] = ws.KmsKeyArn
	}

	if ws.Tags != nil {
		body["tags"] = ws.Tags
	}

	writeJSON(w, body)
}

func (h *Handler) describeWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	ws, err := h.aps.DescribeWorkspace(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"workspace": workspaceToWire(ws)})
}

func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.aps.DeleteWorkspace(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	workspaces, next, err := h.aps.ListWorkspaces(r.Context(), q.Get("alias"), driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(workspaces))
	for i := range workspaces {
		summaries = append(summaries, workspaceSummaryToWire(&workspaces[i]))
	}

	body := map[string]any{"workspaces": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}

// serveAlias handles POST /workspaces/{id}/alias (UpdateWorkspaceAlias).
func (h *Handler) serveAlias(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	if err := h.aps.UpdateWorkspaceAlias(r.Context(), id, stringField(raw, "alias")); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

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
