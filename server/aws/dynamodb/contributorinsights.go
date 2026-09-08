package dynamodb

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

const contributorInsightsActionEnable = "ENABLE"

// routeContributorInsights dispatches the Contributor Insights operations.
// Without them Update/Describe/ListContributorInsights return
// UnknownOperationException.
func (h *Handler) routeContributorInsights(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "UpdateContributorInsights":
		h.updateContributorInsights(w, r)
	case "DescribeContributorInsights":
		h.describeContributorInsights(w, r)
	case "ListContributorInsights":
		h.listContributorInsights(w, r)
	default:
		return false
	}

	return true
}

// contributorInsighter returns the driver's ContributorInsighter capability,
// writing an error response and returning false when it is unsupported.
func (h *Handler) contributorInsighter(w http.ResponseWriter) (dbdriver.ContributorInsighter, bool) {
	c, ok := h.db.(dbdriver.ContributorInsighter)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "contributor insights are not supported by this driver")

		return nil, false
	}

	return c, true
}

func (h *Handler) updateContributorInsights(w http.ResponseWriter, r *http.Request) {
	c, ok := h.contributorInsighter(w)
	if !ok {
		return
	}

	var req struct {
		TableName                 string `json:"TableName"`
		IndexName                 string `json:"IndexName"`
		ContributorInsightsAction string `json:"ContributorInsightsAction"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	enable := req.ContributorInsightsAction == contributorInsightsActionEnable

	status, err := c.UpdateContributorInsights(r.Context(), req.TableName, req.IndexName, enable)
	if err != nil {
		writeContributorInsightsErr(w, err)
		return
	}

	resp := map[string]any{"TableName": req.TableName, "ContributorInsightsStatus": status}
	if req.IndexName != "" {
		resp["IndexName"] = req.IndexName
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) describeContributorInsights(w http.ResponseWriter, r *http.Request) {
	c, ok := h.contributorInsighter(w)
	if !ok {
		return
	}

	var req struct {
		TableName string `json:"TableName"`
		IndexName string `json:"IndexName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	status, lastUpdate, err := c.DescribeContributorInsights(r.Context(), req.TableName, req.IndexName)
	if err != nil {
		writeContributorInsightsErr(w, err)
		return
	}

	resp := map[string]any{
		"TableName":                   req.TableName,
		"ContributorInsightsStatus":   status,
		"ContributorInsightsRuleList": []any{},
	}
	if req.IndexName != "" {
		resp["IndexName"] = req.IndexName
	}

	if lastUpdate != 0 {
		resp["LastUpdateDateTime"] = lastUpdate
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listContributorInsights(w http.ResponseWriter, r *http.Request) {
	c, ok := h.contributorInsighter(w)
	if !ok {
		return
	}

	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	summaries, err := c.ListContributorInsights(r.Context(), req.TableName)
	if err != nil {
		writeContributorInsightsErr(w, err)
		return
	}

	rendered := make([]map[string]any, 0, len(summaries))

	for i := range summaries {
		block := map[string]any{
			"TableName":                 summaries[i].Table,
			"ContributorInsightsStatus": summaries[i].Status,
		}
		if summaries[i].Index != "" {
			block["IndexName"] = summaries[i].Index
		}

		rendered = append(rendered, block)
	}

	wire.WriteJSON(w, map[string]any{"ContributorInsightsSummaries": rendered})
}

// writeContributorInsightsErr maps a provider not-found (missing table or index)
// to ResourceNotFoundException.
func writeContributorInsightsErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", errMessage(err))
		return
	}

	writeErr(w, err)
}
