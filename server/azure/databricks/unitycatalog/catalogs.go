package unitycatalog

import "net/http"

// catalogView is the JSON shape returned for a single catalog (CatalogInfo).
type catalogView struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Comment  string `json:"comment,omitempty"`
}

// createCatalogRequest is the POST /catalogs body (CreateCatalog).
type createCatalogRequest struct {
	Name    string `json:"name"`
	Comment string `json:"comment"`
}

// updateCatalogRequest is the PATCH /catalogs/{name} body (UpdateCatalog).
type updateCatalogRequest struct {
	NewName string `json:"new_name"`
	Comment string `json:"comment"`
}

// listCatalogsResponse is the GET /catalogs body.
type listCatalogsResponse struct {
	Catalogs []catalogView `json:"catalogs"`
}

// serveCatalogs routes the /catalogs collection and /catalogs/{name} item.
func (h *Handler) serveCatalogs(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		h.serveCatalogCollection(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCatalog(w, name)
	case http.MethodPatch:
		h.updateCatalog(w, r, name)
	case http.MethodDelete:
		h.deleteCatalog(w, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveCatalogCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createCatalog(w, r)
	case http.MethodGet:
		h.listCatalogs(w)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createCatalog(w http.ResponseWriter, r *http.Request) {
	var req createCatalogRequest
	if !decode(w, r, &req) {
		return
	}

	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, codeInvalidParam, "name is required")

		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.catalogs[req.Name]; ok {
		writeErr(w, http.StatusConflict, codeAlreadyExists, "catalog already exists: "+req.Name)

		return
	}

	c := &catalog{name: req.Name, comment: req.Comment}
	h.catalogs[req.Name] = c

	writeJSON(w, catalogToView(c))
}

func (h *Handler) getCatalog(w http.ResponseWriter, name string) {
	h.mu.RLock()
	c, ok := h.catalogs[name]
	h.mu.RUnlock()

	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "catalog does not exist: "+name)

		return
	}

	writeJSON(w, catalogToView(c))
}

func (h *Handler) listCatalogs(w http.ResponseWriter) {
	h.mu.RLock()

	views := make([]catalogView, 0, len(h.catalogs))
	for _, c := range h.catalogs {
		views = append(views, catalogToView(c))
	}
	h.mu.RUnlock()

	writeJSON(w, listCatalogsResponse{Catalogs: views})
}

func (h *Handler) updateCatalog(w http.ResponseWriter, r *http.Request, name string) {
	var req updateCatalogRequest
	if !decode(w, r, &req) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	c, ok := h.catalogs[name]
	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "catalog does not exist: "+name)

		return
	}

	if req.Comment != "" {
		c.comment = req.Comment
	}

	if req.NewName != "" && req.NewName != name {
		delete(h.catalogs, name)

		c.name = req.NewName
		h.catalogs[req.NewName] = c
	}

	writeJSON(w, catalogToView(c))
}

func (h *Handler) deleteCatalog(w http.ResponseWriter, name string) {
	h.mu.Lock()

	_, ok := h.catalogs[name]
	if ok {
		delete(h.catalogs, name)
	}
	h.mu.Unlock()

	if !ok {
		writeErr(w, http.StatusNotFound, codeNotFound, "catalog does not exist: "+name)

		return
	}

	writeJSON(w, struct{}{})
}

func catalogToView(c *catalog) catalogView {
	return catalogView{Name: c.name, FullName: c.name, Comment: c.comment}
}
