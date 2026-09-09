package backup

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// Path-tail depths below /backup/plans that select a plan sub-collection or a
// single selection.
const (
	depthPlanSub       = 2
	depthSelectionItem = 3
)

// servePlans routes the /backup/plans tree (below the /backup/plans prefix).
func (h *Handler) servePlans(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.servePlanCollection(w, r)
	case 1:
		h.servePlanItem(w, r, rest[0])
	case depthPlanSub:
		h.servePlanSub(w, r, rest[0], rest[1])
	case depthSelectionItem:
		h.serveSelectionItem(w, r, rest[0], rest[1], rest[2])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// servePlanCollection routes PUT /backup/plans (create) and GET /backup/plans (list).
func (h *Handler) servePlanCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		h.createPlan(w, r)
	case http.MethodGet:
		h.listPlans(w, r)
	default:
		methodNotAllowed(w)
	}
}

// servePlanItem routes verb-keyed operations on a single plan.
func (h *Handler) servePlanItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getPlan(w, r, id)
	case http.MethodPost:
		h.updatePlan(w, r, id)
	case http.MethodDelete:
		h.deletePlan(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// servePlanSub routes the /versions and /selections sub-collections of a plan.
func (h *Handler) servePlanSub(w http.ResponseWriter, r *http.Request, id, sub string) {
	switch sub {
	case segVersions:
		if r.Method == http.MethodGet {
			h.listPlanVersions(w, r, id)

			return
		}

		methodNotAllowed(w)
	case segSelections:
		h.serveSelectionCollection(w, r, id)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackupPlan       planBodyJSON      `json:"BackupPlan"`
		BackupPlanTags   map[string]string `json:"BackupPlanTags"`
		CreatorRequestID string            `json:"CreatorRequestId"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	p, err := h.backup.CreateBackupPlan(r.Context(), &driver.CreatePlanInput{
		Body:             fromPlanBody(&req.BackupPlan),
		Tags:             req.BackupPlanTags,
		CreatorRequestID: req.CreatorRequestID,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, planCreateResponse(p))
}

func (h *Handler) getPlan(w http.ResponseWriter, r *http.Request, id string) {
	p, v, err := h.backup.GetBackupPlan(r.Context(), id, r.URL.Query().Get("versionId"))
	if err != nil {
		writeErr(w, err)

		return
	}

	body := planCreateResponse(p)
	body["BackupPlan"] = toPlanBody(&v.Body)
	body["CreationDate"] = epochSeconds(v.CreationDate)
	body["VersionId"] = v.VersionID

	putString(body, "CreatorRequestId", p.CreatorRequestID)

	if d := epochSeconds(p.LastExecutionDate); d != nil {
		body["LastExecutionDate"] = d
	}

	writeJSON(w, body)
}

func (h *Handler) updatePlan(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		BackupPlan planBodyJSON `json:"BackupPlan"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	p, err := h.backup.UpdateBackupPlan(r.Context(), &driver.UpdatePlanInput{
		BackupPlanID: id,
		Body:         fromPlanBody(&req.BackupPlan),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, planCreateResponse(p))
}

func (h *Handler) deletePlan(w http.ResponseWriter, r *http.Request, id string) {
	p, err := h.backup.DeleteBackupPlan(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"BackupPlanId":  p.BackupPlanID,
		"BackupPlanArn": p.BackupPlanArn,
		"VersionId":     p.Current().VersionID,
	})
}

func (h *Handler) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, next, err := h.backup.ListBackupPlans(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]plansListMemberJSON, 0, len(plans))

	for _, p := range plans {
		cur := p.Current()
		list = append(list, toPlansListMember(p, &cur))
	}

	body := map[string]any{"BackupPlansList": list}
	putString(body, "NextToken", next)
	writeJSON(w, body)
}

func (h *Handler) listPlanVersions(w http.ResponseWriter, r *http.Request, id string) {
	p, versions, next, err := h.backup.ListBackupPlanVersions(r.Context(), id, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	list := make([]plansListMemberJSON, 0, len(versions))
	for i := range versions {
		list = append(list, toPlansListMember(p, &versions[i]))
	}

	body := map[string]any{"BackupPlanVersionsList": list}
	putString(body, "NextToken", next)
	writeJSON(w, body)
}

// planCreateResponse builds the fields shared by CreateBackupPlan and
// UpdateBackupPlan (and the metadata subset of GetBackupPlan), all drawn from
// the plan's current version.
func planCreateResponse(p *driver.Plan) map[string]any {
	cur := p.Current()
	body := map[string]any{
		"BackupPlanArn": p.BackupPlanArn,
		"BackupPlanId":  p.BackupPlanID,
		"CreationDate":  epochSeconds(cur.CreationDate),
		"VersionId":     cur.VersionID,
	}

	if adv := toAdvanced(cur.Body.AdvancedBackupSettings); adv != nil {
		body["AdvancedBackupSettings"] = adv
	}

	return body
}
