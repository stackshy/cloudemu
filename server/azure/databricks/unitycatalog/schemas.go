package unitycatalog

import "net/http"

// schemaView is the JSON shape returned for a single schema (SchemaInfo).
type schemaView struct {
	Name        string `json:"name"`
	CatalogName string `json:"catalog_name"`
	FullName    string `json:"full_name"`
	Comment     string `json:"comment,omitempty"`
}

// createSchemaRequest is the POST /schemas body (CreateSchema).
type createSchemaRequest struct {
	Name        string `json:"name"`
	CatalogName string `json:"catalog_name"`
	Comment     string `json:"comment"`
}

// updateSchemaRequest is the PATCH /schemas/{full_name} body (UpdateSchema).
type updateSchemaRequest struct {
	NewName string `json:"new_name"`
	Comment string `json:"comment"`
}

// listSchemasResponse is the GET /schemas body.
type listSchemasResponse struct {
	Schemas []schemaView `json:"schemas"`
}

// serveSchemas routes the /schemas collection and /schemas/{full_name} item.
func (h *Handler) serveSchemas(w http.ResponseWriter, r *http.Request, fullName string) {
	if fullName == "" {
		h.serveSchemaCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSchema(w, fullName)
	case http.MethodPatch:
		h.updateSchema(w, r, fullName)
	case http.MethodDelete:
		h.deleteSchema(w, fullName)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveSchemaCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createSchema(w, r)
	case http.MethodGet:
		h.listSchemas(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createSchema(w http.ResponseWriter, r *http.Request) {
	var req createSchemaRequest
	if !decode(w, r, &req) {
		return
	}

	if req.Name == "" || req.CatalogName == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "name and catalog_name are required")

		return
	}

	full := req.CatalogName + "." + req.Name

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.catalogs[req.CatalogName]; !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "catalog does not exist: "+req.CatalogName)

		return
	}

	if _, ok := h.schemas[full]; ok {
		writeErr(w, http.StatusConflict, codeAlreadyExists, "schema already exists: "+full)

		return
	}

	s := &schema{name: req.Name, catalogName: req.CatalogName, comment: req.Comment}
	h.schemas[full] = s

	writeJSON(w, schemaToView(s))
}

func (h *Handler) getSchema(w http.ResponseWriter, fullName string) {
	h.mu.RLock()
	s, ok := h.schemas[fullName]
	h.mu.RUnlock()

	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "schema does not exist: "+fullName)

		return
	}

	writeJSON(w, schemaToView(s))
}

func (h *Handler) listSchemas(w http.ResponseWriter, r *http.Request) {
	catalogName := r.URL.Query().Get("catalog_name")

	h.mu.RLock()

	views := make([]schemaView, 0, len(h.schemas))

	for _, s := range h.schemas {
		if catalogName != "" && s.catalogName != catalogName {
			continue
		}

		views = append(views, schemaToView(s))
	}
	h.mu.RUnlock()

	writeJSON(w, listSchemasResponse{Schemas: views})
}

func (h *Handler) updateSchema(w http.ResponseWriter, r *http.Request, fullName string) {
	var req updateSchemaRequest
	if !decode(w, r, &req) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.schemas[fullName]
	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "schema does not exist: "+fullName)

		return
	}

	if req.Comment != "" {
		s.comment = req.Comment
	}

	if req.NewName != "" && req.NewName != s.name {
		delete(h.schemas, fullName)

		s.name = req.NewName
		h.schemas[s.catalogName+"."+s.name] = s
	}

	writeJSON(w, schemaToView(s))
}

func (h *Handler) deleteSchema(w http.ResponseWriter, fullName string) {
	h.mu.Lock()

	_, ok := h.schemas[fullName]
	if ok {
		delete(h.schemas, fullName)
	}
	h.mu.Unlock()

	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "schema does not exist: "+fullName)

		return
	}

	writeJSON(w, struct{}{})
}

func schemaToView(s *schema) schemaView {
	return schemaView{
		Name:        s.name,
		CatalogName: s.catalogName,
		FullName:    s.catalogName + "." + s.name,
		Comment:     s.comment,
	}
}
