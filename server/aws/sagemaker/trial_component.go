package sagemaker

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

type wireTCStatus struct {
	PrimaryStatus string `json:"PrimaryStatus,omitempty"`
	Message       string `json:"Message,omitempty"`
}

type wireTCParam struct {
	StringValue *string  `json:"StringValue,omitempty"`
	NumberValue *float64 `json:"NumberValue,omitempty"`
}

type wireTCArtifact struct {
	Value     string `json:"Value"`
	MediaType string `json:"MediaType,omitempty"`
}

type wireMetadata struct {
	CommitID    string `json:"CommitId,omitempty"`
	GeneratedBy string `json:"GeneratedBy,omitempty"`
	ProjectID   string `json:"ProjectId,omitempty"`
	Repository  string `json:"Repository,omitempty"`
}

func toMetadata(m *wireMetadata) *driver.MetadataProperties {
	if m == nil {
		return nil
	}

	return &driver.MetadataProperties{CommitID: m.CommitID, GeneratedBy: m.GeneratedBy, ProjectID: m.ProjectID, Repository: m.Repository}
}

func fromMetadata(m *driver.MetadataProperties) *wireMetadata {
	if m == nil {
		return nil
	}

	return &wireMetadata{CommitID: m.CommitID, GeneratedBy: m.GeneratedBy, ProjectID: m.ProjectID, Repository: m.Repository}
}

func toTCStatus(s *wireTCStatus) *driver.TrialComponentStatus {
	if s == nil {
		return nil
	}

	return &driver.TrialComponentStatus{PrimaryStatus: s.PrimaryStatus, Message: s.Message}
}

func toTCParams(in map[string]wireTCParam) map[string]driver.TrialComponentParameter {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.TrialComponentParameter, len(in))
	for k, p := range in {
		out[k] = driver.TrialComponentParameter{StringValue: p.StringValue, NumberValue: p.NumberValue}
	}

	return out
}

func toTCArtifacts(in map[string]wireTCArtifact) map[string]driver.TrialComponentArtifact {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.TrialComponentArtifact, len(in))
	for k, a := range in {
		out[k] = driver.TrialComponentArtifact{Value: a.Value, MediaType: a.MediaType}
	}

	return out
}

func fromTCParams(in map[string]driver.TrialComponentParameter) map[string]wireTCParam {
	out := make(map[string]wireTCParam, len(in))
	for k, p := range in {
		out[k] = wireTCParam{StringValue: p.StringValue, NumberValue: p.NumberValue}
	}

	return out
}

func fromTCArtifacts(in map[string]driver.TrialComponentArtifact) map[string]wireTCArtifact {
	out := make(map[string]wireTCArtifact, len(in))
	for k, a := range in {
		out[k] = wireTCArtifact{Value: a.Value, MediaType: a.MediaType}
	}

	return out
}

type wireTCRequest struct {
	TrialComponentName      string                    `json:"TrialComponentName"`
	DisplayName             *string                   `json:"DisplayName"`
	StartTime               *float64                  `json:"StartTime"`
	EndTime                 *float64                  `json:"EndTime"`
	Status                  *wireTCStatus             `json:"Status"`
	Parameters              map[string]wireTCParam    `json:"Parameters"`
	InputArtifacts          map[string]wireTCArtifact `json:"InputArtifacts"`
	OutputArtifacts         map[string]wireTCArtifact `json:"OutputArtifacts"`
	MetadataProperties      *wireMetadata             `json:"MetadataProperties"`
	Tags                    []wireTag                 `json:"Tags"`
	ParametersToRemove      []string                  `json:"ParametersToRemove"`
	InputArtifactsToRemove  []string                  `json:"InputArtifactsToRemove"`
	OutputArtifactsToRemove []string                  `json:"OutputArtifactsToRemove"`
}

func strPtrIf(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

func (h *Handler) createTrialComponent(w http.ResponseWriter, r *http.Request) {
	var req wireTCRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tc, err := h.svc.CreateTrialComponent(r.Context(), driver.TrialComponentSpec{
		Name: req.TrialComponentName, DisplayName: strPtrIf(req.DisplayName), StartTime: isoFrom(req.StartTime), EndTime: isoFrom(req.EndTime),
		Status: toTCStatus(req.Status), Parameters: toTCParams(req.Parameters), InputArtifacts: toTCArtifacts(req.InputArtifacts),
		OutputArtifacts: toTCArtifacts(req.OutputArtifacts), Metadata: toMetadata(req.MetadataProperties), Tags: toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrialComponentArn": tc.ARN})
}

func tcSummaryToWire(tc *driver.TrialComponent) map[string]any {
	out := map[string]any{
		"TrialComponentName": tc.Name, "TrialComponentArn": tc.ARN,
		"CreationTime": epoch(tc.CreationTime), "LastModifiedTime": epoch(tc.LastModifiedTime),
	}
	setIf(out, "DisplayName", tc.DisplayName)
	putTime(out, "StartTime", tc.StartTime)
	putTime(out, "EndTime", tc.EndTime)

	if tc.Status != nil {
		out["Status"] = wireTCStatus{PrimaryStatus: tc.Status.PrimaryStatus, Message: tc.Status.Message}
	}

	if tc.SourceARN != "" {
		out["TrialComponentSource"] = map[string]any{"SourceArn": tc.SourceARN, "SourceType": tc.SourceType}
	}

	return out
}

func (h *Handler) describeTrialComponent(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "TrialComponentName")
	if !ok {
		return
	}

	tc, err := h.svc.DescribeTrialComponent(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	resp := tcSummaryToWire(tc)
	delete(resp, "TrialComponentSource")
	resp["Parameters"] = fromTCParams(tc.Parameters)
	resp["InputArtifacts"] = fromTCArtifacts(tc.InputArtifacts)
	resp["OutputArtifacts"] = fromTCArtifacts(tc.OutputArtifacts)
	resp["Metrics"] = []any{}

	if tc.SourceARN != "" {
		resp["Source"] = map[string]any{"SourceArn": tc.SourceARN, "SourceType": tc.SourceType}
	}

	if md := fromMetadata(tc.Metadata); md != nil {
		resp["MetadataProperties"] = md
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) listTrialComponents(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	page, err := h.svc.ListTrialComponents(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "TrialComponentSummaries", page, tcSummaryToWire)
}

func (h *Handler) updateTrialComponent(w http.ResponseWriter, r *http.Request) {
	var req wireTCRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	up := driver.TrialComponentUpdate{
		Name: req.TrialComponentName, DisplayName: req.DisplayName, Status: toTCStatus(req.Status),
		Parameters: toTCParams(req.Parameters), ParametersToRemove: req.ParametersToRemove,
		InputArtifacts: toTCArtifacts(req.InputArtifacts), InputArtifactsToRemove: req.InputArtifactsToRemove,
		OutputArtifacts: toTCArtifacts(req.OutputArtifacts), OutputArtifactsToRemove: req.OutputArtifactsToRemove,
	}

	if req.StartTime != nil {
		s := isoFrom(req.StartTime)
		up.StartTime = &s
	}

	if req.EndTime != nil {
		s := isoFrom(req.EndTime)
		up.EndTime = &s
	}

	tc, err := h.svc.UpdateTrialComponent(r.Context(), up)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrialComponentArn": tc.ARN})
}

func (h *Handler) deleteTrialComponent(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "TrialComponentName")
	if !ok {
		return
	}

	tc, err := h.svc.DeleteTrialComponent(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrialComponentArn": tc.ARN})
}

// associateTrialComponent and disassociateTrialComponent share a request shape.
func (h *Handler) associateTrialComponent(w http.ResponseWriter, r *http.Request) {
	h.linkTrialComponent(w, r, h.svc.AssociateTrialComponent)
}

func (h *Handler) disassociateTrialComponent(w http.ResponseWriter, r *http.Request) {
	h.linkTrialComponent(w, r, h.svc.DisassociateTrialComponent)
}

//nolint:revive // SDK-shaped; see nongoals/standards
func (h *Handler) linkTrialComponent(w http.ResponseWriter, r *http.Request,
	call func(ctx context.Context, component, trial string) (string, string, error),
) {
	var req struct {
		TrialComponentName string `json:"TrialComponentName"`
		TrialName          string `json:"TrialName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	compARN, trialARN, err := call(r.Context(), req.TrialComponentName, req.TrialName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrialComponentArn": compARN, "TrialArn": trialARN})
}
