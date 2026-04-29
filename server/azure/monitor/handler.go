// Package monitor implements the Azure microsoft.insights metric-alerts
// resource against a CloudEmu monitoring driver. Real armmonitor clients
// hit this handler the same way they hit management.azure.com.
package monitor

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/errors"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName    = "microsoft.insights"
	typeAlerts      = "metricAlerts"
	armNameTag      = "cloudemu:azureAlertName"
	defaultLocation = "global"
)

// Handler serves microsoft.insights ARM resources.
type Handler struct {
	mon mondriver.Monitoring
}

// New returns a monitor handler.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{mon: m}
}

// Matches returns true for ARM URLs targeting microsoft.insights/metricAlerts.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == typeAlerts
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.list(w, r, rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, rp)
	case http.MethodGet:
		h.get(w, r, rp)
	case http.MethodDelete:
		h.delete(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req alertRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cfg := mondriver.AlarmConfig{
		Name:               rp.ResourceName,
		Namespace:          "azure",
		MetricName:         "metric",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          0,
		Period:             60,
		EvaluationPeriods:  1,
		Stat:               "Average",
	}

	if err := h.mon.CreateAlarm(r.Context(), cfg); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLocation
	}

	azurearm.WriteJSON(w, http.StatusCreated, toAlertResponse(rp, loc, req))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if err := alarmExists(r.Context(), h.mon, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAlertResponse(rp, defaultLocation, alertRequest{}))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	alarms, err := h.mon.DescribeAlarms(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := alertListResponse{}

	for i := range alarms {
		scope := rp
		scope.ResourceName = alarms[i].Name
		out.Value = append(out.Value, toAlertResponse(scope, defaultLocation, alertRequest{}))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if err := alarmExists(r.Context(), h.mon, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.mon.DeleteAlarm(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// alarmExists returns nil if an alarm with the given name exists in the
// driver, NotFound otherwise. Callers only need presence/absence.
func alarmExists(ctx context.Context, m mondriver.Monitoring, name string) error {
	alarms, err := m.DescribeAlarms(ctx, nil)
	if err != nil {
		return err
	}

	for i := range alarms {
		if alarms[i].Name == name {
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "metricAlert %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value
func toAlertResponse(rp azurearm.ResourcePath, location string, req alertRequest) alertResponse {
	return alertResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeAlerts, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typeAlerts,
		Location: location,
		Tags:     req.Tags,
		Properties: alertResponseProps{
			alertRequestProps: req.Properties,
			ProvisioningState: "Succeeded",
		},
	}
}
