package logging

import (
	"encoding/json"
	"net/http"
	"time"

	logprovider "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// servePush serves the loggingingestion plane: PutLogs, the only operation it
// publishes. Ingestion is synchronous in real OCI, so it records no work
// request.
func (h *Handler) servePush(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.ID == "" || rt.Sub != subActions || rt.SubID != actionPush {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound,
			"the ingestion API publishes only POST /"+versionIngestion+"/logs/{logId}/actions/push")

		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req putLogsRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.SpecVersion == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "specversion is required")
		return
	}

	batches := make([]logprovider.LogEntryBatch, 0, len(req.LogEntryBatches))

	for i := range req.LogEntryBatches {
		batch, err := toProviderBatch(&req.LogEntryBatches[i])
		if err != nil {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, err.Error())
			return
		}

		batches = append(batches, batch)
	}

	if err := h.extras.PutLogs(r.Context(), rt.ID, batches); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

// toProviderBatch converts one wire batch, rejecting a timestamp it cannot read.
func toProviderBatch(b *putLogsBatch) (logprovider.LogEntryBatch, error) {
	out := logprovider.LogEntryBatch{
		Entries: make([]logprovider.LogEntryItem, 0, len(b.Entries)),
		Source:  b.Source,
		Type:    b.Type,
		Subject: b.Subject,
	}

	defaultTime, err := parseTime(b.DefaultLogEntryTime, "defaultlogentrytime")
	if err != nil {
		return logprovider.LogEntryBatch{}, err
	}

	out.DefaultLogEntryTime = defaultTime

	for i := range b.Entries {
		when, entryErr := parseTime(b.Entries[i].Time, "entry time")
		if entryErr != nil {
			return logprovider.LogEntryBatch{}, entryErr
		}

		out.Entries = append(out.Entries, logprovider.LogEntryItem{
			ID:   b.Entries[i].ID,
			Data: b.Entries[i].Data,
			Time: when,
		})
	}

	return out, nil
}

// serveSearch serves the loggingsearch plane: SearchLogs.
func (h *Handler) serveSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req searchLogsRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	start, err := parseTime(req.TimeStart, "timeStart")
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, err.Error())
		return
	}

	end, err := parseTime(req.TimeEnd, "timeEnd")
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, err.Error())
		return
	}

	result, err := h.extras.SearchLogs(r.Context(), logprovider.SearchRequest{
		Query:           req.SearchQuery,
		TimeStart:       start,
		TimeEnd:         end,
		Limit:           ocirest.Limit(r),
		ReturnFieldInfo: req.IsReturnFieldInfo,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toSearchResponse(result))
}

func toSearchResponse(result *logprovider.SearchResult) searchLogsResponse {
	out := searchLogsResponse{
		Results: make([]searchResult, 0, len(result.Entries)),
		Fields:  make([]fieldInfo, 0, len(result.Fields)),
	}

	for i := range result.Entries {
		out.Results = append(out.Results, toSearchResult(&result.Entries[i]))
	}

	for _, f := range result.Fields {
		out.Fields = append(out.Fields, fieldInfo{FieldName: f.Name, FieldType: f.Type})
	}

	out.Summary = searchSummary{ResultCount: len(out.Results), FieldCount: len(out.Fields)}

	if len(out.Fields) == 0 {
		out.Fields = nil
	}

	return out
}

func toSearchResult(e *logprovider.SearchEntry) searchResult {
	return searchResult{Data: searchResultData{
		Datetime: e.Time.UnixMilli(),
		LogContent: logContent{
			Data: decodePayload(e.Data),
			ID:   e.ID,
			Oracle: oracleFields{
				CompartmentID: e.CompartmentID,
				IngestedTime:  e.IngestedTime.UTC().Format(time.RFC3339),
				LogGroupID:    e.LogGroupID,
				LogID:         e.LogID,
			},
			Source:      e.Source,
			SpecVersion: specVersionOCI,
			Subject:     e.Subject,
			Time:        e.Time.UTC().Format(time.RFC3339),
			Type:        e.Type,
		},
	}}
}

// decodePayload returns a JSON entry payload as an object, matching what real
// OCI does, and anything else as the raw string.
func decodePayload(data string) any {
	var obj map[string]any

	if err := json.Unmarshal([]byte(data), &obj); err == nil {
		return obj
	}

	return data
}

// parseTime reads an OCI timestamp, treating an empty one as unset.
func parseTime(value, field string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, &timeError{field: field, value: value}
	}

	return t, nil
}

// timeError reports a timestamp the handler could not read.
type timeError struct {
	field string
	value string
}

func (e *timeError) Error() string {
	return e.field + " " + e.value + " is not an RFC 3339 timestamp"
}
