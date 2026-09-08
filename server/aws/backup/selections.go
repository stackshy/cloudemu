package backup

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// serveSelectionCollection routes PUT (create) and GET (list) on a plan's
// /selections sub-collection.
func (h *Handler) serveSelectionCollection(w http.ResponseWriter, r *http.Request, planID string) {
	switch r.Method {
	case http.MethodPut:
		h.createSelection(w, r, planID)
	case http.MethodGet:
		h.listSelections(w, r, planID)
	default:
		methodNotAllowed(w)
	}
}

// serveSelectionItem routes verb-keyed operations on a single selection at
// /backup/plans/{planID}/selections/{selectionID}.
func (h *Handler) serveSelectionItem(w http.ResponseWriter, r *http.Request, planID, sub, selectionID string) {
	if sub != segSelections {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSelection(w, r, planID, selectionID)
	case http.MethodDelete:
		h.deleteSelection(w, r, planID, selectionID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createSelection(w http.ResponseWriter, r *http.Request, planID string) {
	var req struct {
		BackupSelection  selectionJSON `json:"BackupSelection"`
		CreatorRequestID string        `json:"CreatorRequestId"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	s, err := h.backup.CreateBackupSelection(r.Context(), &driver.CreateSelectionInput{
		BackupPlanID:     planID,
		Body:             fromSelectionBody(&req.BackupSelection),
		CreatorRequestID: req.CreatorRequestID,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"BackupPlanId": s.BackupPlanID,
		"CreationDate": epochSeconds(s.CreationDate),
		"SelectionId":  s.SelectionID,
	})
}

func (h *Handler) getSelection(w http.ResponseWriter, r *http.Request, planID, selectionID string) {
	s, err := h.backup.GetBackupSelection(r.Context(), planID, selectionID)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{
		"BackupPlanId":    s.BackupPlanID,
		"SelectionId":     s.SelectionID,
		"CreationDate":    epochSeconds(s.CreationDate),
		"BackupSelection": toSelectionBody(&s.Body),
	}
	putString(body, "CreatorRequestId", s.CreatorRequestID)
	writeJSON(w, body)
}

func (h *Handler) deleteSelection(w http.ResponseWriter, r *http.Request, planID, selectionID string) {
	if err := h.backup.DeleteBackupSelection(r.Context(), planID, selectionID); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"BackupPlanId": planID, "SelectionId": selectionID})
}

func (h *Handler) listSelections(w http.ResponseWriter, r *http.Request, planID string) {
	selections, next, err := h.backup.ListBackupSelections(r.Context(), planID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]map[string]any, 0, len(selections))

	for _, s := range selections {
		item := map[string]any{
			"SelectionId":  s.SelectionID,
			"BackupPlanId": s.BackupPlanID,
			"CreationDate": epochSeconds(s.CreationDate),
		}
		putString(item, "SelectionName", s.Body.SelectionName)
		putString(item, "IamRoleArn", s.Body.IamRoleArn)
		putString(item, "CreatorRequestId", s.CreatorRequestID)
		list = append(list, item)
	}

	body := map[string]any{"BackupSelectionsList": list}
	putString(body, "NextToken", next)
	writeJSON(w, body)
}
