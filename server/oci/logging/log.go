package logging

import (
	"net/http"

	logprovider "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveLogs maps method and path shape onto the log operations nested under a
// log group.
func (h *Handler) serveLogs(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.SubID == "" {
		switch r.Method {
		case http.MethodPost:
			h.createLog(w, r, rt.ID)
		case http.MethodGet:
			h.listLogs(w, r, rt.ID)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getLog(w, r, rt.ID, rt.SubID)
	case http.MethodPut:
		h.updateLog(w, r, rt.ID, rt.SubID)
	case http.MethodDelete:
		h.deleteLog(w, r, rt.ID, rt.SubID)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) createLog(w http.ResponseWriter, r *http.Request, groupID string) {
	if !h.requireWork(w, r) {
		return
	}

	var req createLogRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	// Real OCI defaults isEnabled to true; an absent field must not silently
	// create a log that drops everything ingested into it.
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}

	spec := logprovider.LogSpec{
		DisplayName:   req.DisplayName,
		LogType:       req.LogType,
		IsEnabled:     enabled,
		Configuration: toProviderConfiguration(req.Configuration),
		FreeformTags:  req.FreeformTags,
	}

	if req.RetentionDuration != nil {
		spec.RetentionDuration = *req.RetentionDuration
	}

	l, err := h.extras.CreateLog(r.Context(), groupID, spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationCreateLog, l.CompartmentID, entityTypeLog, workrequest.ActionCreated, l.ID)
}

// listLogs lists the logs in a group. OCI takes no compartmentId here — the
// group in the path fixes the compartment — so the log group OCID is the
// required parameter and the query narrows by log attributes only.
func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request, groupID string) {
	q := r.URL.Query()

	logs, err := h.extras.ListLogs(r.Context(), groupID, logprovider.LogFilter{
		DisplayName:    q.Get("displayName"),
		LogType:        q.Get("logType"),
		SourceService:  q.Get("sourceService"),
		SourceResource: q.Get("sourceResource"),
		LifecycleState: q.Get("lifecycleState"),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]logResponse, 0, len(logs))
	for i := range logs {
		out = append(out, toLogResponse(&logs[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

func (h *Handler) getLog(w http.ResponseWriter, r *http.Request, groupID, logID string) {
	l, err := h.extras.GetLog(r.Context(), groupID, logID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toLogResponse(l))
}

func (h *Handler) updateLog(w http.ResponseWriter, r *http.Request, groupID, logID string) {
	if !h.requireWork(w, r) {
		return
	}

	var req updateLogRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	l, err := h.extras.UpdateLog(r.Context(), groupID, logID, logprovider.LogUpdate{
		DisplayName:       req.DisplayName,
		IsEnabled:         req.IsEnabled,
		RetentionDuration: req.RetentionDuration,
		Configuration:     toProviderConfiguration(req.Configuration),
		FreeformTags:      req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationUpdateLog, l.CompartmentID, entityTypeLog, workrequest.ActionUpdated, l.ID)
}

func (h *Handler) deleteLog(w http.ResponseWriter, r *http.Request, groupID, logID string) {
	if !h.requireWork(w, r) {
		return
	}

	l, err := h.extras.GetLog(r.Context(), groupID, logID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.extras.DeleteLog(r.Context(), groupID, logID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationDeleteLog, l.CompartmentID, entityTypeLog, workrequest.ActionDeleted, logID)
}

func toProviderConfiguration(cfg *logConfigurationBody) *logprovider.LogConfiguration {
	if cfg == nil {
		return nil
	}

	out := &logprovider.LogConfiguration{
		CompartmentID: cfg.CompartmentID,
		Source: logprovider.LogSource{
			SourceType: cfg.Source.SourceType,
			Service:    cfg.Source.Service,
			Resource:   cfg.Source.Resource,
			Category:   cfg.Source.Category,
			Parameters: cfg.Source.Parameters,
		},
	}

	if cfg.Archiving != nil {
		out.ArchivingEnabled = cfg.Archiving.IsEnabled
	}

	return out
}

func toLogResponse(l *logprovider.Log) logResponse {
	out := logResponse{
		ID:                l.ID,
		LogGroupID:        l.LogGroupID,
		CompartmentID:     l.CompartmentID,
		DisplayName:       l.DisplayName,
		LogType:           l.LogType,
		IsEnabled:         l.IsEnabled,
		LifecycleState:    l.LifecycleState,
		RetentionDuration: l.RetentionDuration,
		TimeCreated:       l.TimeCreated,
		TimeLastModified:  l.TimeLastModified,
		FreeformTags:      orEmptyTags(l.FreeformTags),
		DefinedTags:       definedTags{},
	}

	if l.Configuration != nil {
		out.Configuration = &logConfigurationBody{
			CompartmentID: l.Configuration.CompartmentID,
			Source: logSourceBody{
				SourceType: l.Configuration.Source.SourceType,
				Service:    l.Configuration.Source.Service,
				Resource:   l.Configuration.Source.Resource,
				Category:   l.Configuration.Source.Category,
				Parameters: l.Configuration.Source.Parameters,
			},
			Archiving: &archivingBody{IsEnabled: l.Configuration.ArchivingEnabled},
		}
	}

	return out
}
