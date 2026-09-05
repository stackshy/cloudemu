package cloudwatch

// The AWS CLI and the Terraform AWS provider speak CloudWatch's classic query
// protocol (form-encoded POST, XML responses). This file adds the query-protocol
// dashboard operations (backing aws_cloudwatch_dashboard) and DescribeAlarmHistory
// so those clients work — the rpc-v2-cbor twins live in dashboards.go and
// metric_data_ops.go.

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"
)

func (h *Handler) queryPutDashboard(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "dashboards not supported")
		return
	}

	if err := store.PutDashboard(r.Context(), r.Form.Get("DashboardName"), r.Form.Get("DashboardBody")); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "PutDashboardResponse", putDashboardResultXML{})
}

func (h *Handler) queryGetDashboard(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "dashboards not supported")
		return
	}

	d, err := store.GetDashboard(r.Context(), r.Form.Get("DashboardName"))
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "GetDashboardResponse", getDashboardResultXML{
		DashboardArn:  d.ARN,
		DashboardName: d.Name,
		DashboardBody: d.Body,
	})
}

func (h *Handler) queryListDashboards(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "dashboards not supported")
		return
	}

	entries, err := store.ListDashboards(r.Context(), r.Form.Get("DashboardNamePrefix"))
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	from, to, next := pageWindow(len(entries), decodeOffsetToken(r.Form.Get("NextToken")), dashboardPageSize)

	rows := make([]dashboardEntryXML, 0, to-from)
	for _, e := range entries[from:to] {
		rows = append(rows, dashboardEntryXML{
			DashboardName: e.Name,
			DashboardArn:  e.ARN,
			LastModified:  e.LastModified.UTC().Format(time.RFC3339),
			Size:          int64(e.Size),
		})
	}

	result := listDashboardsResultXML{DashboardEntries: rows}
	if next > 0 {
		result.NextToken = encodeOffsetToken(next)
	}

	writeQueryResponse(w, "ListDashboardsResponse", result)
}

func (h *Handler) queryDeleteDashboards(w http.ResponseWriter, r *http.Request) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeQueryError(w, http.StatusBadRequest, "InvalidAction", "dashboards not supported")
		return
	}

	if err := store.DeleteDashboards(r.Context(), queryStringList(r, "DashboardNames.member.")); err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	writeQueryResponse(w, "DeleteDashboardsResponse", deleteDashboardsResultXML{})
}

// queryDescribeAlarmHistory is the query-protocol twin of describeAlarmHistory,
// reusing the shared filter/paging helpers so ordering and MaxRecords behave
// identically to the rpc-v2-cbor path.
func (h *Handler) queryDescribeAlarmHistory(w http.ResponseWriter, r *http.Request) {
	in := describeAlarmHistoryInput{
		AlarmName:       r.Form.Get("AlarmName"),
		HistoryItemType: r.Form.Get("HistoryItemType"),
		ScanBy:          r.Form.Get("ScanBy"),
		StartDate:       queryOptTime(r, "StartDate"),
		EndDate:         queryOptTime(r, "EndDate"),
		NextToken:       r.Form.Get("NextToken"),
	}
	if v, _ := strconv.Atoi(r.Form.Get("MaxRecords")); v > 0 {
		in.MaxRecords = v
	}

	entries, err := h.monitoring.GetAlarmHistory(r.Context(), in.AlarmName, 0)
	if err != nil {
		writeQueryDriverErr(w, err)
		return
	}

	items, next := pageAlarmHistory(filterAlarmHistory(entries, &in), &in)

	members := make([]alarmHistoryMemberXML, 0, len(items))
	for i := range items {
		members = append(members, alarmHistoryMemberXML{
			AlarmName:       items[i].AlarmName,
			Timestamp:       items[i].Timestamp.UTC().Format(time.RFC3339),
			HistoryItemType: items[i].HistoryItemType,
			HistorySummary:  items[i].HistorySummary,
			HistoryData:     items[i].HistoryData,
		})
	}

	result := describeAlarmHistoryResultXML{AlarmHistoryItems: members}
	if next != "" {
		result.NextToken = next
	}

	writeQueryResponse(w, "DescribeAlarmHistoryResponse", result)
}

// queryOptTime parses an ISO8601 timestamp form field, returning nil when absent
// or unparseable so callers treat it as an open bound.
func queryOptTime(r *http.Request, field string) *time.Time {
	raw := r.Form.Get(field)
	if raw == "" {
		return nil
	}

	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}

	return &t
}

// ---- XML response shapes (query protocol, 2010-08-01) ----

type putDashboardResultXML struct {
	XMLName                     xml.Name `xml:"PutDashboardResult"`
	DashboardValidationMessages struct{} `xml:"DashboardValidationMessages"`
}

type getDashboardResultXML struct {
	XMLName       xml.Name `xml:"GetDashboardResult"`
	DashboardArn  string   `xml:"DashboardArn"`
	DashboardName string   `xml:"DashboardName"`
	DashboardBody string   `xml:"DashboardBody"`
}

type dashboardEntryXML struct {
	DashboardName string `xml:"DashboardName"`
	DashboardArn  string `xml:"DashboardArn"`
	LastModified  string `xml:"LastModified"`
	Size          int64  `xml:"Size"`
}

type listDashboardsResultXML struct {
	XMLName          xml.Name            `xml:"ListDashboardsResult"`
	DashboardEntries []dashboardEntryXML `xml:"DashboardEntries>member"`
	NextToken        string              `xml:"NextToken,omitempty"`
}

type deleteDashboardsResultXML struct {
	XMLName xml.Name `xml:"DeleteDashboardsResult"`
}

type alarmHistoryMemberXML struct {
	AlarmName       string `xml:"AlarmName"`
	Timestamp       string `xml:"Timestamp"`
	HistoryItemType string `xml:"HistoryItemType"`
	HistorySummary  string `xml:"HistorySummary,omitempty"`
	HistoryData     string `xml:"HistoryData,omitempty"`
}

type describeAlarmHistoryResultXML struct {
	XMLName           xml.Name                `xml:"DescribeAlarmHistoryResult"`
	AlarmHistoryItems []alarmHistoryMemberXML `xml:"AlarmHistoryItems>member"`
	NextToken         string                  `xml:"NextToken,omitempty"`
}
