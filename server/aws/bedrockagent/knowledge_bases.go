package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// serveKnowledgeBases dispatches the /knowledgebases subtree, including the
// nested data-source and ingestion-job paths.
func (h *Handler) serveKnowledgeBases(w http.ResponseWriter, r *http.Request, segs []string) {
	switch {
	case len(segs) == 0:
		h.serveKBCollection(w, r)
	case len(segs) == 1:
		h.serveKBItem(w, r, segs[0])
	case segs[1] == segDataSources:
		h.serveDataSources(w, r, segs)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveKBCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		h.createKnowledgeBase(w, r)
	case http.MethodPost:
		h.listKnowledgeBases(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveKBItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getKnowledgeBase(w, r, id)
	case http.MethodPut:
		h.updateKnowledgeBase(w, r, id)
	case http.MethodDelete:
		h.deleteKnowledgeBase(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveDataSources dispatches /knowledgebases/{kb}/datasources[/{ds}[/ingestionjobs/]].
func (h *Handler) serveDataSources(w http.ResponseWriter, r *http.Request, segs []string) {
	kbID := segs[0]

	switch {
	case len(segs) == dsCollectionSegments:
		h.serveDSCollection(w, r, kbID)
	case len(segs) == dsItemSegments:
		h.serveDSItem(w, r, kbID, segs[2])
	case len(segs) == ingestionSegments && segs[3] == segIngestionJobs:
		h.serveIngestion(w, r, kbID, segs[2])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveDSCollection(w http.ResponseWriter, r *http.Request, kbID string) {
	switch r.Method {
	case http.MethodPut:
		h.createDataSource(w, r, kbID)
	case http.MethodPost:
		h.listDataSources(w, r, kbID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveDSItem(w http.ResponseWriter, r *http.Request, kbID, dsID string) {
	switch r.Method {
	case http.MethodGet:
		h.getDataSource(w, r, kbID, dsID)
	case http.MethodPut:
		h.updateDataSource(w, r, kbID, dsID)
	case http.MethodDelete:
		h.deleteDataSource(w, r, kbID, dsID)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveIngestion(w http.ResponseWriter, r *http.Request, kbID, dsID string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	h.startIngestionJob(w, r, kbID, dsID)
}

// --- knowledge-base operations ---

func (h *Handler) createKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	var in createKnowledgeBaseRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	kb, err := h.agent.CreateKnowledgeBase(r.Context(), badriver.KnowledgeBaseConfig{
		Name:                       in.Name,
		RoleArn:                    in.RoleArn,
		Description:                in.Description,
		KnowledgeBaseConfiguration: in.KnowledgeBaseConfiguration,
		StorageConfiguration:       in.StorageConfiguration,
		Tags:                       in.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, knowledgeBaseEnvelope{KnowledgeBase: toKnowledgeBaseJSON(kb)})
}

func (h *Handler) getKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	kb, err := h.agent.GetKnowledgeBase(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, knowledgeBaseEnvelope{KnowledgeBase: toKnowledgeBaseJSON(kb)})
}

func (h *Handler) listKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	kbs, err := h.agent.ListKnowledgeBases(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]knowledgeBaseSummaryJSON, 0, len(kbs))
	for i := range kbs {
		out = append(out, toKnowledgeBaseSummaryJSON(&kbs[i]))
	}

	writeJSON(w, listKnowledgeBasesResponse{KnowledgeBaseSummaries: out})
}

func (h *Handler) updateKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	var in createKnowledgeBaseRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	kb, err := h.agent.UpdateKnowledgeBase(r.Context(), id, badriver.KnowledgeBaseConfig{
		Name:                       in.Name,
		RoleArn:                    in.RoleArn,
		Description:                in.Description,
		KnowledgeBaseConfiguration: in.KnowledgeBaseConfiguration,
		StorageConfiguration:       in.StorageConfiguration,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, knowledgeBaseEnvelope{KnowledgeBase: toKnowledgeBaseJSON(kb)})
}

func (h *Handler) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	status, err := h.agent.DeleteKnowledgeBase(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deleteKnowledgeBaseResponse{KnowledgeBaseID: id, Status: status})
}

// --- data-source operations ---

func (h *Handler) createDataSource(w http.ResponseWriter, r *http.Request, kbID string) {
	var in createDataSourceRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	ds, err := h.agent.CreateDataSource(r.Context(), badriver.DataSourceConfig{
		KnowledgeBaseID:              kbID,
		Name:                         in.Name,
		Description:                  in.Description,
		DataDeletionPolicy:           in.DataDeletionPolicy,
		DataSourceConfiguration:      in.DataSourceConfiguration,
		VectorIngestionConfiguration: in.VectorIngestionConfiguration,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, dataSourceEnvelope{DataSource: toDataSourceJSON(ds)})
}

func (h *Handler) getDataSource(w http.ResponseWriter, r *http.Request, kbID, dsID string) {
	ds, err := h.agent.GetDataSource(r.Context(), kbID, dsID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, dataSourceEnvelope{DataSource: toDataSourceJSON(ds)})
}

func (h *Handler) listDataSources(w http.ResponseWriter, r *http.Request, kbID string) {
	dss, err := h.agent.ListDataSources(r.Context(), kbID)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]dataSourceSummaryJSON, 0, len(dss))
	for i := range dss {
		out = append(out, toDataSourceSummaryJSON(&dss[i]))
	}

	writeJSON(w, listDataSourcesResponse{DataSourceSummaries: out})
}

func (h *Handler) updateDataSource(w http.ResponseWriter, r *http.Request, kbID, dsID string) {
	var in createDataSourceRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	ds, err := h.agent.UpdateDataSource(r.Context(), badriver.DataSourceConfig{
		KnowledgeBaseID:         kbID,
		Name:                    in.Name,
		Description:             in.Description,
		DataDeletionPolicy:      in.DataDeletionPolicy,
		DataSourceConfiguration: in.DataSourceConfiguration,
	}, dsID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, dataSourceEnvelope{DataSource: toDataSourceJSON(ds)})
}

func (h *Handler) deleteDataSource(w http.ResponseWriter, r *http.Request, kbID, dsID string) {
	status, err := h.agent.DeleteDataSource(r.Context(), kbID, dsID)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deleteDataSourceResponse{DataSourceID: dsID, KnowledgeBaseID: kbID, Status: status})
}

func (h *Handler) startIngestionJob(w http.ResponseWriter, r *http.Request, kbID, dsID string) {
	var in startIngestionJobRequest
	if !decodeBody(w, r, &in) {
		return
	}

	job, err := h.agent.StartIngestionJob(r.Context(), kbID, dsID, in.Description)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, ingestionJobEnvelope{IngestionJob: toIngestionJobJSON(job)})
}

// --- converters ---

func toKnowledgeBaseJSON(kb *badriver.KnowledgeBase) knowledgeBaseJSON {
	return knowledgeBaseJSON{
		KnowledgeBaseID:            kb.ID,
		KnowledgeBaseARN:           kb.ARN,
		Name:                       kb.Name,
		RoleArn:                    kb.RoleArn,
		Description:                kb.Description,
		Status:                     kb.Status,
		KnowledgeBaseConfiguration: kb.KnowledgeBaseConfiguration,
		StorageConfiguration:       kb.StorageConfiguration,
		CreatedAt:                  kb.CreatedAt,
		UpdatedAt:                  kb.UpdatedAt,
	}
}

func toKnowledgeBaseSummaryJSON(kb *badriver.KnowledgeBase) knowledgeBaseSummaryJSON {
	return knowledgeBaseSummaryJSON{
		KnowledgeBaseID: kb.ID,
		Name:            kb.Name,
		Status:          kb.Status,
		Description:     kb.Description,
		UpdatedAt:       kb.UpdatedAt,
	}
}

func toDataSourceJSON(ds *badriver.DataSource) dataSourceJSON {
	return dataSourceJSON{
		DataSourceID:            ds.ID,
		KnowledgeBaseID:         ds.KnowledgeBaseID,
		Name:                    ds.Name,
		Description:             ds.Description,
		Status:                  ds.Status,
		DataDeletionPolicy:      ds.DataDeletionPolicy,
		DataSourceConfiguration: ds.DataSourceConfiguration,
		CreatedAt:               ds.CreatedAt,
		UpdatedAt:               ds.UpdatedAt,
	}
}

func toDataSourceSummaryJSON(ds *badriver.DataSource) dataSourceSummaryJSON {
	return dataSourceSummaryJSON{
		DataSourceID:    ds.ID,
		KnowledgeBaseID: ds.KnowledgeBaseID,
		Name:            ds.Name,
		Status:          ds.Status,
		Description:     ds.Description,
		UpdatedAt:       ds.UpdatedAt,
	}
}

func toIngestionJobJSON(j *badriver.IngestionJob) ingestionJobJSON {
	return ingestionJobJSON{
		IngestionJobID:  j.ID,
		KnowledgeBaseID: j.KnowledgeBaseID,
		DataSourceID:    j.DataSourceID,
		Description:     j.Description,
		Status:          j.Status,
		StartedAt:       j.StartedAt,
		UpdatedAt:       j.UpdatedAt,
	}
}
