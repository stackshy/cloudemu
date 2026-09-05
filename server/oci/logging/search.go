package logging

import (
	"encoding/json"
	"net/http"
	"time"

	logprovider "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

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
