package sagemaker

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// scheduleConfigView extracts the members of a MonitoringScheduleConfig the
// provider validates; the rest of the config is stored and echoed verbatim.
type scheduleConfigView struct {
	MonitoringJobDefinition *struct {
		MonitoringInputs []struct {
			EndpointInput *struct {
				EndpointName string `json:"EndpointName"`
			} `json:"EndpointInput"`
		} `json:"MonitoringInputs"`
	} `json:"MonitoringJobDefinition"`
	MonitoringJobDefinitionName string `json:"MonitoringJobDefinitionName"`
	MonitoringType              string `json:"MonitoringType"`
	ScheduleConfig              *struct {
		ScheduleExpression    string `json:"ScheduleExpression"`
		DataAnalysisStartTime string `json:"DataAnalysisStartTime"`
		DataAnalysisEndTime   string `json:"DataAnalysisEndTime"`
	} `json:"ScheduleConfig"`
}

func scheduleSpecFrom(name string, config json.RawMessage, tags []wireTag) (driver.MonitoringScheduleSpec, error) {
	var v scheduleConfigView
	if len(config) > 0 {
		if err := json.Unmarshal(config, &v); err != nil {
			return driver.MonitoringScheduleSpec{}, err
		}
	}

	spec := driver.MonitoringScheduleSpec{
		Name: name, Type: v.MonitoringType, JobDefinitionName: v.MonitoringJobDefinitionName, Config: config, Tags: toTags(tags),
	}

	if sc := v.ScheduleConfig; sc != nil {
		spec.ScheduleExpression, spec.DataAnalysisStartTime, spec.DataAnalysisEndTime = sc.ScheduleExpression, sc.DataAnalysisStartTime, sc.DataAnalysisEndTime //nolint:lll // SDK-shaped literal
	}

	if d := v.MonitoringJobDefinition; d != nil {
		for _, in := range d.MonitoringInputs {
			if in.EndpointInput != nil && in.EndpointInput.EndpointName != "" {
				spec.EndpointName = in.EndpointInput.EndpointName

				break
			}
		}
	}

	return spec, nil
}

func (*Handler) decodeScheduleRequest(w http.ResponseWriter, r *http.Request) (driver.MonitoringScheduleSpec, bool) {
	var req struct {
		MonitoringScheduleName   string          `json:"MonitoringScheduleName"`
		MonitoringScheduleConfig json.RawMessage `json:"MonitoringScheduleConfig"`
		Tags                     []wireTag       `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return driver.MonitoringScheduleSpec{}, false
	}

	spec, err := scheduleSpecFrom(req.MonitoringScheduleName, req.MonitoringScheduleConfig, req.Tags)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", err.Error())

		return driver.MonitoringScheduleSpec{}, false
	}

	return spec, true
}

func (h *Handler) createMonitoringSchedule(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.decodeScheduleRequest(w, r)
	if !ok {
		return
	}

	s, err := h.svc.CreateMonitoringSchedule(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"MonitoringScheduleArn": s.ARN})
}

func (h *Handler) updateMonitoringSchedule(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.decodeScheduleRequest(w, r)
	if !ok {
		return
	}

	s, err := h.svc.UpdateMonitoringSchedule(r.Context(), spec)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"MonitoringScheduleArn": s.ARN})
}

func executionToWire(e *driver.MonitoringExecution) map[string]any {
	out := map[string]any{
		"MonitoringScheduleName": e.ScheduleName, "ScheduledTime": epoch(e.ScheduledTime), "CreationTime": epoch(e.CreationTime),
		"LastModifiedTime": epoch(e.LastModifiedTime), "MonitoringExecutionStatus": e.Status,
	}
	setIf(out, "EndpointName", e.EndpointName)
	setIf(out, "FailureReason", e.FailureReason)
	setIf(out, "MonitoringJobDefinitionName", e.JobDefinitionName)
	setIf(out, "MonitoringType", e.Type)
	setIf(out, "ProcessingJobArn", e.ProcessingJobARN)

	return out
}

func (h *Handler) describeMonitoringSchedule(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "MonitoringScheduleName")
	if !ok {
		return
	}

	s, err := h.svc.DescribeMonitoringSchedule(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := map[string]any{
		"MonitoringScheduleArn": s.ARN, "MonitoringScheduleName": s.Name, "MonitoringScheduleStatus": s.Status,
		"MonitoringScheduleConfig": s.Config, "CreationTime": epoch(s.CreationTime), "LastModifiedTime": epoch(s.LastModifiedTime),
	}
	setIf(resp, "EndpointName", s.EndpointName)
	setIf(resp, "FailureReason", s.FailureReason)
	setIf(resp, "MonitoringType", s.Type)

	if s.LastExecution != nil {
		resp["LastMonitoringExecutionSummary"] = executionToWire(s.LastExecution)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listMonitoringSchedules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		listReq
		EndpointName                string `json:"EndpointName"`
		MonitoringJobDefinitionName string `json:"MonitoringJobDefinitionName"`
		MonitoringTypeEquals        string `json:"MonitoringTypeEquals"`
	}

	if !wire.DecodeJSON(w, r, &req) || rejectZeroMaxResults(w, &req.listReq) {
		return
	}

	opts := req.options()
	opts.Filters["EndpointName"], opts.Filters["MonitoringJobDefinitionName"], opts.Filters["MonitoringTypeEquals"] =
		req.EndpointName, req.MonitoringJobDefinitionName, req.MonitoringTypeEquals

	page, err := h.svc.ListMonitoringSchedules(r.Context(), opts)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "MonitoringScheduleSummaries", page, func(s *driver.MonitoringSchedule) map[string]any {
		m := map[string]any{
			"MonitoringScheduleName": s.Name, "MonitoringScheduleArn": s.ARN, "MonitoringScheduleStatus": s.Status,
			"CreationTime": epoch(s.CreationTime), "LastModifiedTime": epoch(s.LastModifiedTime),
		}
		setIf(m, "EndpointName", s.EndpointName)
		setIf(m, "MonitoringJobDefinitionName", s.JobDefinitionName)
		setIf(m, "MonitoringType", s.Type)

		return m
	})
}

func (h *Handler) deleteMonitoringSchedule(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "MonitoringScheduleName", h.svc.DeleteMonitoringSchedule)
}

func (h *Handler) startMonitoringSchedule(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "MonitoringScheduleName", h.svc.StartMonitoringSchedule)
}

func (h *Handler) stopMonitoringSchedule(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "MonitoringScheduleName", h.svc.StopMonitoringSchedule)
}

func (h *Handler) listMonitoringExecutions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		listReq
		MonitoringScheduleName      string   `json:"MonitoringScheduleName"`
		MonitoringJobDefinitionName string   `json:"MonitoringJobDefinitionName"`
		MonitoringTypeEquals        string   `json:"MonitoringTypeEquals"`
		EndpointName                string   `json:"EndpointName"`
		ScheduledTimeAfter          *float64 `json:"ScheduledTimeAfter"`
		ScheduledTimeBefore         *float64 `json:"ScheduledTimeBefore"`
	}

	if !wire.DecodeJSON(w, r, &req) || rejectZeroMaxResults(w, &req.listReq) {
		return
	}

	opts := req.options()

	for k, v := range map[string]string{
		"MonitoringScheduleName": req.MonitoringScheduleName, "MonitoringJobDefinitionName": req.MonitoringJobDefinitionName,
		"MonitoringTypeEquals": req.MonitoringTypeEquals, "EndpointName": req.EndpointName,
		"ScheduledTimeAfter": isoFrom(req.ScheduledTimeAfter), "ScheduledTimeBefore": isoFrom(req.ScheduledTimeBefore),
	} {
		if v != "" {
			opts.Filters[k] = v
		}
	}

	page, err := h.svc.ListMonitoringExecutions(r.Context(), opts)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "MonitoringExecutionSummaries", page, executionToWire)
}

// --- Alerts ---

func (h *Handler) listMonitoringAlerts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		listReq
		MonitoringScheduleName string `json:"MonitoringScheduleName"`
	}

	if !wire.DecodeJSON(w, r, &req) || rejectZeroMaxResults(w, &req.listReq) {
		return
	}

	page, err := h.svc.ListMonitoringAlerts(r.Context(), req.MonitoringScheduleName, req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "MonitoringAlertSummaries", page, func(a *driver.MonitoringAlert) map[string]any {
		return map[string]any{
			"MonitoringAlertName": a.Name, "CreationTime": epoch(a.CreationTime), "LastModifiedTime": epoch(a.LastModifiedTime),
			"AlertStatus": a.Status, "DatapointsToAlert": a.DatapointsToAlert, "EvaluationPeriod": a.EvaluationPeriod,
			"Actions": map[string]any{"ModelDashboardIndicator": map[string]any{"Enabled": a.DashboardIndicator}},
		}
	})
}

func (h *Handler) listMonitoringAlertHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		listReq
		MonitoringScheduleName string `json:"MonitoringScheduleName"`
		MonitoringAlertName    string `json:"MonitoringAlertName"`
	}

	if !wire.DecodeJSON(w, r, &req) || rejectZeroMaxResults(w, &req.listReq) {
		return
	}

	opts := req.options()
	opts.Filters["MonitoringScheduleName"], opts.Filters["MonitoringAlertName"] = req.MonitoringScheduleName, req.MonitoringAlertName

	page, err := h.svc.ListMonitoringAlertHistory(r.Context(), opts)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "MonitoringAlertHistory", page, func(e *driver.MonitoringAlertEvent) map[string]any {
		return map[string]any{
			"MonitoringScheduleName": e.ScheduleName, "MonitoringAlertName": e.AlertName,
			"CreationTime": epoch(e.CreationTime), "AlertStatus": e.Status,
		}
	})
}

func (h *Handler) updateMonitoringAlert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MonitoringScheduleName string `json:"MonitoringScheduleName"`
		MonitoringAlertName    string `json:"MonitoringAlertName"`
		DatapointsToAlert      int    `json:"DatapointsToAlert"`
		EvaluationPeriod       int    `json:"EvaluationPeriod"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	s, err := h.svc.UpdateMonitoringAlert(r.Context(), req.MonitoringScheduleName, req.MonitoringAlertName, req.DatapointsToAlert, req.EvaluationPeriod) //nolint:lll // SDK-shaped literal
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"MonitoringScheduleArn": s.ARN, "MonitoringAlertName": req.MonitoringAlertName})
}

// --- Job definitions (four kinds, one set of handlers) ---

// monitoringRoutes returns the 16 job definition operations (Create, Describe,
// List and Delete for each kind).
func (h *Handler) monitoringRoutes() map[string]http.HandlerFunc {
	routes := make(map[string]http.HandlerFunc, len(driver.MonitoringKinds)*routesPerKind)

	for _, kind := range driver.MonitoringKinds {
		routes["Create"+kind+"JobDefinition"] = func(w http.ResponseWriter, r *http.Request) { h.createMonitoringDef(w, r, kind) }
		routes["Describe"+kind+"JobDefinition"] = func(w http.ResponseWriter, r *http.Request) { h.describeMonitoringDef(w, r, kind) }
		routes["List"+kind+"JobDefinitions"] = func(w http.ResponseWriter, r *http.Request) { h.listMonitoringDefs(w, r, kind) }
		routes["Delete"+kind+"JobDefinition"] = func(w http.ResponseWriter, r *http.Request) {
			stopByName(w, r, "JobDefinitionName", func(ctx context.Context, name string) error {
				return h.svc.DeleteMonitoringJobDefinition(ctx, kind, name)
			})
		}
	}

	return routes
}

func (h *Handler) createMonitoringDef(w http.ResponseWriter, r *http.Request, kind string) {
	var raw map[string]json.RawMessage
	if !wire.DecodeJSON(w, r, &raw) {
		return
	}

	var req struct {
		JobDefinitionName string    `json:"JobDefinitionName"`
		RoleArn           string    `json:"RoleArn"`
		Tags              []wireTag `json:"Tags"`
	}

	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &req); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", err.Error())

		return
	}

	delete(raw, "Tags")
	delete(raw, "JobDefinitionName")
	delete(raw, "RoleArn")

	def, err := h.svc.CreateMonitoringJobDefinition(r.Context(), driver.MonitoringJobDefinitionSpec{
		Kind: kind, Name: req.JobDefinitionName, RoleARN: req.RoleArn, EndpointName: definitionEndpoint(kind, raw), Echo: raw, Tags: toTags(req.Tags), //nolint:lll // SDK-shaped literal
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"JobDefinitionArn": def.ARN})
}

// definitionEndpoint finds the endpoint a job definition monitors in its
// <Kind>JobInput.EndpointInput.EndpointName.
func definitionEndpoint(kind string, echo map[string]json.RawMessage) string {
	var in struct {
		EndpointInput *struct {
			EndpointName string `json:"EndpointName"`
		} `json:"EndpointInput"`
	}

	if err := json.Unmarshal(echo[kind+"JobInput"], &in); err != nil || in.EndpointInput == nil {
		return ""
	}

	return in.EndpointInput.EndpointName
}

func (h *Handler) describeMonitoringDef(w http.ResponseWriter, r *http.Request, kind string) {
	name, ok := decodeName1(w, r, "JobDefinitionName")
	if !ok {
		return
	}

	def, err := h.svc.DescribeMonitoringJobDefinition(r.Context(), kind, name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, describeBody(nil, def.Echo, map[string]any{
		"JobDefinitionArn": def.ARN, "JobDefinitionName": def.Name, "RoleArn": def.RoleARN, "CreationTime": epoch(def.CreationTime),
	}))
}

func (h *Handler) listMonitoringDefs(w http.ResponseWriter, r *http.Request, kind string) {
	var req struct {
		listReq
		EndpointName string `json:"EndpointName"`
	}

	if !wire.DecodeJSON(w, r, &req) || rejectZeroMaxResults(w, &req.listReq) {
		return
	}

	opts := req.options()
	if req.EndpointName != "" {
		opts.Filters["EndpointName"] = req.EndpointName
	}

	page, err := h.svc.ListMonitoringJobDefinitions(r.Context(), kind, opts)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "JobDefinitionSummaries", page, func(d *driver.MonitoringJobDefinition) map[string]any {
		m := map[string]any{
			"MonitoringJobDefinitionName": d.Name, "MonitoringJobDefinitionArn": d.ARN, "CreationTime": epoch(d.CreationTime),
		}
		setIf(m, "EndpointName", d.EndpointName)

		return m
	})
}

// routesPerKind is the operations each monitoring job-definition kind serves: create, describe, list, delete.
const routesPerKind = 4
