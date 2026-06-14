package unitycatalog

import "net/http"

// tableView is the JSON shape returned for a single table (TableInfo).
type tableView struct {
	Name        string `json:"name"`
	CatalogName string `json:"catalog_name"`
	SchemaName  string `json:"schema_name"`
	FullName    string `json:"full_name"`
	Comment     string `json:"comment,omitempty"`
}

// createTableRequest is the POST /tables body (CreateTableRequest).
type createTableRequest struct {
	Name        string `json:"name"`
	CatalogName string `json:"catalog_name"`
	SchemaName  string `json:"schema_name"`
	Comment     string `json:"comment"`
}

// listTablesResponse is the GET /tables body.
type listTablesResponse struct {
	Tables []tableView `json:"tables"`
}

// serveTables routes the /tables collection and /tables/{full_name} item.
func (h *Handler) serveTables(w http.ResponseWriter, r *http.Request, fullName string) {
	if fullName == "" {
		h.serveTableCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTable(w, fullName)
	case http.MethodDelete:
		h.deleteTable(w, fullName)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveTableCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createTable(w, r)
	case http.MethodGet:
		h.listTables(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	var req createTableRequest
	if !decode(w, r, &req) {
		return
	}

	if req.Name == "" || req.CatalogName == "" || req.SchemaName == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "name, catalog_name and schema_name are required")

		return
	}

	schemaFull := req.CatalogName + "." + req.SchemaName
	full := schemaFull + "." + req.Name

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.schemas[schemaFull]; !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "schema does not exist: "+schemaFull)

		return
	}

	if _, ok := h.tables[full]; ok {
		writeErr(w, http.StatusConflict, codeAlreadyExists, "table already exists: "+full)

		return
	}

	tbl := &table{name: req.Name, catalogName: req.CatalogName, schemaName: req.SchemaName, comment: req.Comment}
	h.tables[full] = tbl

	writeJSON(w, tableToView(tbl))
}

func (h *Handler) getTable(w http.ResponseWriter, fullName string) {
	h.mu.RLock()
	tbl, ok := h.tables[fullName]
	h.mu.RUnlock()

	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "table does not exist: "+fullName)

		return
	}

	writeJSON(w, tableToView(tbl))
}

func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	catalogName := r.URL.Query().Get("catalog_name")
	schemaName := r.URL.Query().Get("schema_name")

	h.mu.RLock()

	views := make([]tableView, 0, len(h.tables))

	for _, tbl := range h.tables {
		if !tableMatches(tbl, catalogName, schemaName) {
			continue
		}

		views = append(views, tableToView(tbl))
	}
	h.mu.RUnlock()

	writeJSON(w, listTablesResponse{Tables: views})
}

func (h *Handler) deleteTable(w http.ResponseWriter, fullName string) {
	h.mu.Lock()

	_, ok := h.tables[fullName]
	if ok {
		delete(h.tables, fullName)
	}
	h.mu.Unlock()

	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "table does not exist: "+fullName)

		return
	}

	writeJSON(w, struct{}{})
}

func tableMatches(tbl *table, catalogName, schemaName string) bool {
	if catalogName != "" && tbl.catalogName != catalogName {
		return false
	}

	if schemaName != "" && tbl.schemaName != schemaName {
		return false
	}

	return true
}

func tableToView(tbl *table) tableView {
	return tableView{
		Name:        tbl.name,
		CatalogName: tbl.catalogName,
		SchemaName:  tbl.schemaName,
		FullName:    tbl.catalogName + "." + tbl.schemaName + "." + tbl.name,
		Comment:     tbl.comment,
	}
}
