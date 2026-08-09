package sesv2

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveDashboard routes /deliverability-dashboard and its sub-paths.
func (h *Handler) serveDashboard(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		h.serveDashboardRoot(w, r)

		return
	}

	switch rest[0] {
	case "test":
		h.createTestReport(w, r, rest)
	case "test-reports":
		h.serveTestReports(w, r, rest[1:])
	case segCampaigns:
		h.getCampaign(w, r, rest)
	case "domains":
		h.listCampaigns(w, r, rest)
	case "statistics-report":
		h.getStatisticsReport(w, r, rest)
	case "blacklist-report":
		h.getBlacklistReports(w, r, rest)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveDashboardRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		enabled, err := h.ses.GetDeliverabilityDashboardOptions(r.Context())
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, dashboardOptionsResponse{DashboardEnabled: enabled})
	case http.MethodPut:
		var req putDashboardRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		writeOK(w, h.ses.PutDeliverabilityDashboardOption(r.Context(), req.DashboardEnabled))
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTestReport(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	var req createTestReportRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := driver.DeliverabilityTestReportInput{
		ReportName: req.ReportName,
		Subject:    req.Content.subject(),
		Tags:       tagsToMap(req.Tags),
	}
	in.FromEmailAddress = req.FromEmailAddress

	rep, err := h.ses.CreateDeliverabilityTestReport(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createTestReportResponse{
		ReportID:                 rep.ReportID,
		DeliverabilityTestStatus: rep.DeliverabilityStatus,
	})
}

func (h *Handler) serveTestReports(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		reports, err := h.ses.ListDeliverabilityTestReports(r.Context())
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]testReportJSON, 0, len(reports))
		for i := range reports {
			out = append(out, testReportToJSON(&reports[i]))
		}

		writeJSON(w, listTestReportsResponse{DeliverabilityTestReports: out})
	case 1:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		rep, err := h.ses.GetDeliverabilityTestReport(r.Context(), rest[0])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, getTestReportResponse{
			DeliverabilityTestReport: testReportToJSON(rep),
			OverallPlacement:         map[string]any{},
			IspPlacements:            []any{},
		})
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) getCampaign(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != twoSegments || r.Method != http.MethodGet {
		notFound(w, r.URL.Path)

		return
	}

	blob, err := h.ses.GetDomainDeliverabilityCampaign(r.Context(), rest[1])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeRawJSON(w, `{"DomainDeliverabilityCampaign":`+blob+`}`)
}

func (h *Handler) listCampaigns(w http.ResponseWriter, r *http.Request, rest []string) {
	// /domains/{SubscribedDomain}/campaigns
	if len(rest) != campaignPathLen || rest[2] != segCampaigns || r.Method != http.MethodGet {
		notFound(w, r.URL.Path)

		return
	}

	ids, err := h.ses.ListDomainDeliverabilityCampaigns(r.Context(), rest[1])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, listCampaignsResponse{DomainDeliverabilityCampaigns: ids})
}

func (h *Handler) getStatisticsReport(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != twoSegments || r.Method != http.MethodGet {
		notFound(w, r.URL.Path)

		return
	}

	blob, err := h.ses.GetDomainStatisticsReport(r.Context(), rest[1])
	if err != nil {
		writeErr(w, err)

		return
	}

	writeRawJSON(w, blob)
}

func (h *Handler) getBlacklistReports(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 || r.Method != http.MethodGet {
		notFound(w, r.URL.Path)

		return
	}

	ips := r.URL.Query()["BlacklistItemNames"]

	reports, err := h.ses.GetBlacklistReports(r.Context(), ips)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getBlacklistReportsResponse{BlacklistReport: reports})
}

func testReportToJSON(r *driver.DeliverabilityTestReport) testReportJSON {
	return testReportJSON{
		ReportID:                 r.ReportID,
		ReportName:               r.ReportName,
		Subject:                  r.Subject,
		FromEmailAddress:         r.FromEmailAddress,
		CreateDate:               epochSeconds(r.CreatedAt),
		DeliverabilityTestStatus: r.DeliverabilityStatus,
	}
}

// writeRawJSON writes a pre-serialized JSON string with a 200 status.
func writeRawJSON(w http.ResponseWriter, raw string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(raw))
}

// campaignPathLen is the segment count of /domains/{domain}/campaigns below the
// deliverability-dashboard root.
const campaignPathLen = 3

// subject extracts the subject from a raw test-report message content blob.
func (c rawContent) subject() string {
	if len(c) == 0 {
		return ""
	}

	var m struct {
		Simple struct {
			Subject struct {
				Data string `json:"Data"`
			} `json:"Subject"`
		} `json:"Simple"`
	}

	_ = json.Unmarshal(c, &m)

	return m.Simple.Subject.Data
}
