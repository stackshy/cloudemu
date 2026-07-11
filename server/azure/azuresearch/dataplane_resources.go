package azuresearch

import (
	"net/http"

	srchdriver "github.com/stackshy/cloudemu/azuresearch/driver"
)

// --- Indexers ---

func indexerJSON(i *srchdriver.Indexer) map[string]any {
	return map[string]any{
		"name": i.Name, "dataSourceName": i.DataSourceName, "targetIndexName": i.TargetIndex,
		"skillsetName": i.SkillsetName, "schedule": map[string]any{"interval": i.Schedule}, "@odata.etag": i.ETag,
	}
}

func (h *DataPlaneHandler) serveIndexers(w http.ResponseWriter, r *http.Request, service string, parts []string) {
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			ixs, err := h.dp.ListIndexers(r.Context(), service)
			writeDPList(w, indexerJSON, ixs, err)
		case http.MethodPost:
			h.putIndexer(w, r, service, "")
		default:
			dpMethodNotAllowed(w)
		}

		return
	}

	name := parts[1]

	// /indexers/{name}/{run|reset|status}
	const actionIdx = 2
	if len(parts) > actionIdx {
		h.indexerAction(w, r, service, name, parts[actionIdx])

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putIndexer(w, r, service, name)
	case http.MethodGet:
		ix, err := h.dp.GetIndexer(r.Context(), service, name)
		writeDP(w, indexerJSON, ix, err)
	case http.MethodDelete:
		dpDelete(w, h.dp.DeleteIndexer(r.Context(), service, name))
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) putIndexer(w http.ResponseWriter, r *http.Request, service, name string) {
	var body struct {
		Name            string `json:"name"`
		DataSourceName  string `json:"dataSourceName"`
		TargetIndexName string `json:"targetIndexName"`
		SkillsetName    string `json:"skillsetName"`
		Schedule        struct {
			Interval string `json:"interval"`
		} `json:"schedule"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	if body.Name == "" {
		body.Name = name
	}

	ix, err := h.dp.CreateOrUpdateIndexer(r.Context(), service, srchdriver.IndexerConfig{
		Name: body.Name, DataSourceName: body.DataSourceName, TargetIndex: body.TargetIndexName,
		SkillsetName: body.SkillsetName, Schedule: body.Schedule.Interval,
	})
	writeDP(w, indexerJSON, ix, err)
}

func (h *DataPlaneHandler) indexerAction(w http.ResponseWriter, r *http.Request, service, name, action string) {
	switch action {
	case "run":
		dpDelete(w, h.dp.RunIndexer(r.Context(), service, name))
	case "reset":
		dpDelete(w, h.dp.ResetIndexer(r.Context(), service, name))
	case "status":
		st, err := h.dp.GetIndexerStatus(r.Context(), service, name)
		if err != nil {
			dpCErr(w, err)

			return
		}

		dpJSON(w, map[string]any{
			"name": st.Name, "status": st.Status,
			"lastResult": map[string]any{
				"status": st.LastResult, "itemsProcessed": st.ItemsProcessed, "itemsFailed": st.ItemsFailed,
			},
		})
	default:
		dpErr(w, http.StatusNotFound, "unknown indexer action: "+action)
	}
}

// --- Data sources ---

func dataSourceJSON(d *srchdriver.DataSource) map[string]any {
	return map[string]any{
		"name": d.Name, "type": d.Type,
		"container":   map[string]any{"name": d.Container},
		"@odata.etag": d.ETag,
	}
}

//nolint:dupl // uniform data-plane collection CRUD; shape recurs across resources.
func (h *DataPlaneHandler) serveDataSources(w http.ResponseWriter, r *http.Request, service string, parts []string) {
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			ds, err := h.dp.ListDataSources(r.Context(), service)
			writeDPList(w, dataSourceJSON, ds, err)
		case http.MethodPost:
			h.putDataSource(w, r, service, "")
		default:
			dpMethodNotAllowed(w)
		}

		return
	}

	name := parts[1]

	switch r.Method {
	case http.MethodPut:
		h.putDataSource(w, r, service, name)
	case http.MethodGet:
		ds, err := h.dp.GetDataSource(r.Context(), service, name)
		writeDP(w, dataSourceJSON, ds, err)
	case http.MethodDelete:
		dpDelete(w, h.dp.DeleteDataSource(r.Context(), service, name))
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) putDataSource(w http.ResponseWriter, r *http.Request, service, name string) {
	var body struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		Container struct {
			Name string `json:"name"`
		} `json:"container"`
		Credentials struct {
			ConnectionString string `json:"connectionString"`
		} `json:"credentials"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	if body.Name == "" {
		body.Name = name
	}

	ds, err := h.dp.CreateOrUpdateDataSource(r.Context(), service, srchdriver.DataSource{
		Name: body.Name, Type: body.Type, Container: body.Container.Name, ConnString: body.Credentials.ConnectionString,
	})
	writeDP(w, dataSourceJSON, ds, err)
}

// --- Skillsets ---

func skillsetJSON(s *srchdriver.Skillset) map[string]any {
	return map[string]any{
		"name": s.Name, "description": s.Description, "skills": make([]any, s.SkillCount), "@odata.etag": s.ETag,
	}
}

//nolint:dupl // uniform data-plane collection CRUD; shape recurs across resources.
func (h *DataPlaneHandler) serveSkillsets(w http.ResponseWriter, r *http.Request, service string, parts []string) {
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			sk, err := h.dp.ListSkillsets(r.Context(), service)
			writeDPList(w, skillsetJSON, sk, err)
		case http.MethodPost:
			h.putSkillset(w, r, service, "")
		default:
			dpMethodNotAllowed(w)
		}

		return
	}

	name := parts[1]

	switch r.Method {
	case http.MethodPut:
		h.putSkillset(w, r, service, name)
	case http.MethodGet:
		sk, err := h.dp.GetSkillset(r.Context(), service, name)
		writeDP(w, skillsetJSON, sk, err)
	case http.MethodDelete:
		dpDelete(w, h.dp.DeleteSkillset(r.Context(), service, name))
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) putSkillset(w http.ResponseWriter, r *http.Request, service, name string) {
	var body struct {
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Skills      []map[string]any `json:"skills"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	if body.Name == "" {
		body.Name = name
	}

	sk, err := h.dp.CreateOrUpdateSkillset(r.Context(), service, srchdriver.Skillset{
		Name: body.Name, Description: body.Description, SkillCount: len(body.Skills),
	})
	writeDP(w, skillsetJSON, sk, err)
}

// --- Synonym maps ---

func synonymMapJSON(s *srchdriver.SynonymMap) map[string]any {
	return map[string]any{"name": s.Name, "format": s.Format, "synonyms": s.Synonyms, "@odata.etag": s.ETag}
}

//nolint:dupl // uniform data-plane collection CRUD; shape recurs across resources.
func (h *DataPlaneHandler) serveSynonymMaps(w http.ResponseWriter, r *http.Request, service string, parts []string) {
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			sm, err := h.dp.ListSynonymMaps(r.Context(), service)
			writeDPList(w, synonymMapJSON, sm, err)
		case http.MethodPost:
			h.putSynonymMap(w, r, service, "")
		default:
			dpMethodNotAllowed(w)
		}

		return
	}

	name := parts[1]

	switch r.Method {
	case http.MethodPut:
		h.putSynonymMap(w, r, service, name)
	case http.MethodGet:
		sm, err := h.dp.GetSynonymMap(r.Context(), service, name)
		writeDP(w, synonymMapJSON, sm, err)
	case http.MethodDelete:
		dpDelete(w, h.dp.DeleteSynonymMap(r.Context(), service, name))
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) putSynonymMap(w http.ResponseWriter, r *http.Request, service, name string) {
	var body struct {
		Name     string `json:"name"`
		Format   string `json:"format"`
		Synonyms string `json:"synonyms"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	if body.Name == "" {
		body.Name = name
	}

	sm, err := h.dp.CreateOrUpdateSynonymMap(r.Context(), service, srchdriver.SynonymMap{
		Name: body.Name, Format: body.Format, Synonyms: body.Synonyms,
	})
	writeDP(w, synonymMapJSON, sm, err)
}

// --- Aliases ---

func aliasJSON(a *srchdriver.Alias) map[string]any {
	return map[string]any{"name": a.Name, "indexes": a.Indexes, "@odata.etag": a.ETag}
}

//nolint:dupl // uniform data-plane collection CRUD; shape recurs across resources.
func (h *DataPlaneHandler) serveAliases(w http.ResponseWriter, r *http.Request, service string, parts []string) {
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			al, err := h.dp.ListAliases(r.Context(), service)
			writeDPList(w, aliasJSON, al, err)
		case http.MethodPost:
			h.putAlias(w, r, service, "")
		default:
			dpMethodNotAllowed(w)
		}

		return
	}

	name := parts[1]

	switch r.Method {
	case http.MethodPut:
		h.putAlias(w, r, service, name)
	case http.MethodGet:
		al, err := h.dp.GetAlias(r.Context(), service, name)
		writeDP(w, aliasJSON, al, err)
	case http.MethodDelete:
		dpDelete(w, h.dp.DeleteAlias(r.Context(), service, name))
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) putAlias(w http.ResponseWriter, r *http.Request, service, name string) {
	var body struct {
		Name    string   `json:"name"`
		Indexes []string `json:"indexes"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	if body.Name == "" {
		body.Name = name
	}

	al, err := h.dp.CreateOrUpdateAlias(r.Context(), service, srchdriver.Alias{Name: body.Name, Indexes: body.Indexes})
	writeDP(w, aliasJSON, al, err)
}

// --- Service statistics ---

func (h *DataPlaneHandler) serveServiceStats(w http.ResponseWriter, r *http.Request, service string) {
	if r.Method != http.MethodGet {
		dpMethodNotAllowed(w)

		return
	}

	st, err := h.dp.GetServiceStatistics(r.Context(), service)
	if err != nil {
		dpCErr(w, err)

		return
	}

	dpJSON(w, map[string]any{
		"counters": map[string]any{
			"documentCount":    map[string]any{"usage": st.DocumentCount},
			"indexesCount":     map[string]any{"usage": st.IndexCount},
			"indexersCount":    map[string]any{"usage": st.IndexerCount},
			"dataSourcesCount": map[string]any{"usage": st.DataSourceCount},
			"storageSize":      map[string]any{"usage": st.StorageBytes},
		},
	})
}

// writeDPList writes a "value"-wrapped list or maps the error.
func writeDPList[T any](w http.ResponseWriter, toJSON func(*T) map[string]any, items []T, err error) {
	if err != nil {
		dpCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, toJSON(&items[i]))
	}

	dpJSON(w, map[string]any{"value": out})
}
