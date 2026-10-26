package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type startReportRequest struct {
	nameArnRequest
	AnalysisType string `json:"AnalysisType"`
}

func (h *Handler) startAnalysisReport(w http.ResponseWriter, r *http.Request) {
	var req startReportRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	reports, ok := capability[nfdriver.AnalysisReports](h, w)
	if !ok {
		return
	}

	rep, err := reports.StartAnalysisReport(r.Context(), req.FirewallName, req.FirewallArn, req.AnalysisType)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"AnalysisReportId": rep.ID})
}

type listReportsRequest struct {
	nameArnRequest
	listRequest
}

func (h *Handler) listAnalysisReports(w http.ResponseWriter, r *http.Request) {
	var req listReportsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	reports, ok := capability[nfdriver.AnalysisReports](h, w)
	if !ok {
		return
	}

	items, next, err := reports.ListAnalysisReports(r.Context(), req.FirewallName, req.FirewallArn, req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, map[string]any{
			"AnalysisReportId": items[i].ID, "AnalysisType": items[i].AnalysisType,
			"ReportTime": epochSeconds(items[i].ReportTime), "Status": items[i].Status,
		})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"AnalysisReports": out}, next))
}

type getReportRequest struct {
	nameArnRequest
	listRequest
	AnalysisReportID string `json:"AnalysisReportId"`
}

func (h *Handler) getAnalysisReportResults(w http.ResponseWriter, r *http.Request) {
	var req getReportRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	reports, ok := capability[nfdriver.AnalysisReports](h, w)
	if !ok {
		return
	}

	rep, err := reports.GetAnalysisReport(r.Context(), req.FirewallName, req.FirewallArn, req.AnalysisReportID)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"AnalysisType": rep.AnalysisType, "Status": rep.Status, "ReportTime": epochSeconds(rep.ReportTime),
		"StartTime": epochSeconds(rep.ReportTime), "EndTime": epochSeconds(rep.ReportTime),
		"AnalysisReportResults": []map[string]any{},
	})
}
