package logging

import (
	"net/http"

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
