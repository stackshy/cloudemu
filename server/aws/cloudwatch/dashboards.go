package cloudwatch

// This file implements the CloudWatch dashboard operations (PutDashboard,
// GetDashboard, ListDashboards, DeleteDashboards) over the rpc-v2-cbor
// protocol, backing the aws_cloudwatch_dashboard Terraform resource. The store
// is an AWS-local optional capability so the shared Monitoring interface — and
// the Azure/GCP providers — stay unchanged.

import (
	"context"
	"net/http"
	"time"

	"github.com/fxamacker/cbor/v2"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// dashboardStore is the AWS-local capability behind the dashboard operations.
type dashboardStore interface {
	PutDashboard(ctx context.Context, name, body string) error
	GetDashboard(ctx context.Context, name string) (*mondriver.DashboardInfo, error)
	ListDashboards(ctx context.Context, prefix string) ([]mondriver.DashboardEntry, error)
	DeleteDashboards(ctx context.Context, names []string) error
}

type putDashboardInput struct {
	DashboardName string `cbor:"DashboardName"`
	DashboardBody string `cbor:"DashboardBody"`
}

// dashboardValidationMessageCBR mirrors the wire shape; cloudemu accepts any
// valid-JSON body, so the list is always empty on success.
type dashboardValidationMessageCBR struct {
	DataPath string `cbor:"DataPath,omitempty"`
	Message  string `cbor:"Message,omitempty"`
}

type putDashboardOutput struct {
	DashboardValidationMessages []dashboardValidationMessageCBR `cbor:"DashboardValidationMessages"`
}

func (h *Handler) putDashboard(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "dashboards not supported")
		return
	}

	var in putDashboardInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := store.PutDashboard(r.Context(), in.DashboardName, in.DashboardBody); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, putDashboardOutput{DashboardValidationMessages: []dashboardValidationMessageCBR{}})
}

type getDashboardInput struct {
	DashboardName string `cbor:"DashboardName"`
}

type getDashboardOutput struct {
	DashboardArn  string `cbor:"DashboardArn"`
	DashboardName string `cbor:"DashboardName"`
	DashboardBody string `cbor:"DashboardBody"`
}

func (h *Handler) getDashboard(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "dashboards not supported")
		return
	}

	var in getDashboardInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	d, err := store.GetDashboard(r.Context(), in.DashboardName)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, getDashboardOutput{
		DashboardArn:  d.ARN,
		DashboardName: d.Name,
		DashboardBody: d.Body,
	})
}

type listDashboardsInput struct {
	DashboardNamePrefix string `cbor:"DashboardNamePrefix,omitempty"`
	NextToken           string `cbor:"NextToken,omitempty"`
}

type dashboardEntryCBR struct {
	DashboardName string    `cbor:"DashboardName"`
	DashboardArn  string    `cbor:"DashboardArn"`
	LastModified  time.Time `cbor:"LastModified"`
	Size          int64     `cbor:"Size"`
}

type listDashboardsOutput struct {
	DashboardEntries []dashboardEntryCBR `cbor:"DashboardEntries"`
	NextToken        string              `cbor:"NextToken,omitempty"`
}

// dashboardPageSize is the number of dashboard entries returned per page.
const dashboardPageSize = 1000

func (h *Handler) listDashboards(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "dashboards not supported")
		return
	}

	var in listDashboardsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	entries, err := store.ListDashboards(r.Context(), in.DashboardNamePrefix)
	if err != nil {
		writeDriverErr(w, err)
		return
	}

	from, to, next := pageWindow(len(entries), decodeOffsetToken(in.NextToken), dashboardPageSize)

	rows := make([]dashboardEntryCBR, 0, to-from)
	for _, e := range entries[from:to] {
		rows = append(rows, dashboardEntryCBR{
			DashboardName: e.Name,
			DashboardArn:  e.ARN,
			LastModified:  e.LastModified.UTC(),
			Size:          int64(e.Size),
		})
	}

	resp := listDashboardsOutput{DashboardEntries: rows}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	writeCBORResponse(w, resp)
}

type deleteDashboardsInput struct {
	DashboardNames []string `cbor:"DashboardNames"`
}

func (h *Handler) deleteDashboards(w http.ResponseWriter, r *http.Request, body []byte) {
	store, ok := h.monitoring.(dashboardStore)
	if !ok {
		writeCBORError(w, http.StatusBadRequest, "UnknownOperationException", "dashboards not supported")
		return
	}

	var in deleteDashboardsInput
	if err := cbor.Unmarshal(body, &in); err != nil {
		writeCBORError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return
	}

	if err := store.DeleteDashboards(r.Context(), in.DashboardNames); err != nil {
		writeDriverErr(w, err)
		return
	}

	writeCBORResponse(w, struct{}{})
}
